package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// maxForced caps the forced skills' instructions in one prompt.
const maxForced = 256 << 10

// enabledSkills are the enabled skills, without bodies.
func (in *instance) enabledSkills(ctx context.Context) ([]skill, error) {
	if in.store == nil {
		return nil, nil
	}
	return in.store.summaries(ctx, true)
}

// forcedSkills loads the skills a run's @skills/ mentions name, each of
// which must be enabled.
func (in *instance) forcedSkills(ctx context.Context, enabled []skill, names []string) ([]skill, error) {
	var forced []skill
	size := 0
	for _, name := range names {
		if slices.ContainsFunc(forced, func(sk skill) bool { return sk.Name == name }) {
			continue
		}
		if !slices.ContainsFunc(enabled, func(sk skill) bool { return sk.Name == name }) {
			return nil, protocol.InvalidParams(fmt.Errorf("unknown skill %q: it isn't stored or isn't enabled", name))
		}
		sk, err := in.store.get(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("load skill %s: %w", name, err)
		}
		if size += len(sk.Body); size > maxForced {
			return nil, protocol.InvalidParams(fmt.Errorf("the mentioned skills' instructions are over %d KiB together; mention fewer", maxForced>>10))
		}
		forced = append(forced, sk)
	}
	return forced, nil
}

// skillsPrompt is the system prompt's section for the forced skills.
func skillsPrompt(forced []skill) string {
	if len(forced) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Skills\n\nThe owner chose these skills for this task. Follow them.")
	for _, sk := range forced {
		fmt.Fprintf(&b, "\n\n### %s\n\n%s", sk.Name, sk.Body)
		if len(sk.Files) > 0 {
			b.WriteString("\n\nReference files: " + strings.Join(slices.Sorted(maps.Keys(sk.Files)), ", "))
		}
	}
	return b.String()
}

// skillTools offers the enabled skills. load_skill's description lists
// them, so a model run without tools never hears of them.
func skillTools(enabled []skill) []protocol.ChatTool {
	var list strings.Builder
	var names []string
	for _, sk := range enabled {
		fmt.Fprintf(&list, "\n- %s: %s", sk.Name, sk.Description)
		names = append(names, sk.Name)
	}
	name := map[string]any{"type": "string", "enum": names}
	return []protocol.ChatTool{
		tool("load_skill", "Load a skill's instructions and the names of its reference files. Load a skill when its description fits the task. Skills:"+list.String(),
			map[string]any{"name": name}, "name"),
		tool("load_skill_file", "Read one of a skill's reference files, by the path load_skill lists or the skill's instructions name.",
			map[string]any{"name": name, "path": map[string]any{"type": "string"}}, "name", "path"),
	}
}

// skillCall answers load_skill and load_skill_file. A bad call goes back to
// the model as the tool's result.
func (in *instance) skillCall(ctx context.Context, enabled []skill, tool, arguments string) (string, error) {
	var args struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "error: the arguments aren't valid JSON", nil
	}
	if !slices.ContainsFunc(enabled, func(sk skill) bool { return sk.Name == args.Name }) {
		return fmt.Sprintf("error: no skill %q", args.Name), nil
	}
	sk, err := in.store.get(ctx, args.Name)
	if errors.Is(err, errSkillNotFound) {
		return fmt.Sprintf("error: skill %q was deleted", args.Name), nil
	}
	if err != nil {
		return "", err
	}
	paths := slices.Sorted(maps.Keys(sk.Files))
	if tool == "load_skill" {
		if len(paths) == 0 {
			return sk.Body, nil
		}
		return sk.Body + "\n\nReference files: " + strings.Join(paths, ", "), nil
	}
	text, ok := sk.Files[args.Path]
	if !ok {
		return fmt.Sprintf("error: skill %s has no file %q; it has: %s", sk.Name, args.Path, strings.Join(paths, ", ")), nil
	}
	return text, nil
}

// tools builds a run's toolbox: the library's tools for a library action,
// and the skill tools when any skill is enabled.
func (in *instance) tools(enabled []skill, lib toolFunc) toolbox {
	var box toolbox
	if lib != nil {
		box.tools = append(box.tools, libraryTools...)
	}
	if len(enabled) > 0 {
		box.tools = append(box.tools, skillTools(enabled)...)
	}
	box.call = func(ctx context.Context, name, arguments string) (string, error) {
		switch {
		case len(enabled) > 0 && (name == "load_skill" || name == "load_skill_file"):
			return in.skillCall(ctx, enabled, name, arguments)
		case lib != nil:
			return lib(ctx, name, arguments)
		}
		return fmt.Sprintf("error: no tool %q", name), nil
	}
	return box
}
