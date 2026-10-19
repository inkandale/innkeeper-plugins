package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/inkandale/innkeeper/protocol"
	"gopkg.in/yaml.v3"
)

// skill is one Agent Skill (https://agentskills.io/specification): a
// SKILL.md's frontmatter and body, and its text reference files by path.
// A new skill is enabled, so an import is usable at once.
type skill struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Body        string            `json:"body"`
	Files       map[string]string `json:"files,omitempty"`
	// Enabled skills reach the model.
	Enabled bool `json:"enabled"`
}

// Import limits. Skills are prompt text, so these are generous for text and
// keep a hostile archive or page from filling memory.
const (
	maxUpload     = 8 << 20 // a SKILL.md or zip, uploaded or fetched
	maxZipEntries = 200
	maxFileSize   = 1 << 20 // SKILL.md or one reference file
	maxSkillSize  = 4 << 20 // body and reference files together
	maxFiles      = 64
	fetchTimeout  = 30 * time.Second
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// check applies the Agent Skills name and description rules and the limits.
func (s skill) check() error {
	if len(s.Name) > 64 || !skillNamePattern.MatchString(s.Name) {
		return protocol.InvalidParams(fmt.Errorf("skill name %q must be 1 to 64 lowercase letters, digits, and single hyphens, not starting or ending with a hyphen", s.Name))
	}
	if d := strings.TrimSpace(s.Description); d == "" || utf8.RuneCountInString(d) > 1024 || !isText([]byte(d)) {
		return protocol.InvalidParams(fmt.Errorf("skill %s: description must be 1 to 1024 characters of text", s.Name))
	}
	if strings.TrimSpace(s.Body) == "" {
		return protocol.InvalidParams(fmt.Errorf("skill %s: instructions are empty", s.Name))
	}
	if !isText([]byte(s.Body)) {
		return protocol.InvalidParams(fmt.Errorf("skill %s: instructions must be UTF-8 text without NUL characters", s.Name))
	}
	if len(s.Body) > maxFileSize {
		return protocol.InvalidParams(fmt.Errorf("skill %s: instructions are over %d MiB", s.Name, maxFileSize>>20))
	}
	if len(s.Files) > maxFiles {
		return protocol.InvalidParams(fmt.Errorf("skill %s has %d reference files; the limit is %d", s.Name, len(s.Files), maxFiles))
	}
	total := len(s.Body)
	for p, text := range s.Files {
		if !isText([]byte(p)) || !localPath(p) || path.Clean(p) != p || p == "SKILL.md" {
			return protocol.InvalidParams(fmt.Errorf("skill %s: reference file path %q must be relative, such as references/notes.md", s.Name, p))
		}
		if !isText([]byte(text)) {
			return protocol.InvalidParams(fmt.Errorf("skill %s: %s must be UTF-8 text without NUL characters", s.Name, p))
		}
		if len(text) > maxFileSize {
			return protocol.InvalidParams(fmt.Errorf("skill %s: %s is over %d MiB", s.Name, p, maxFileSize>>20))
		}
		total += len(text)
	}
	if total > maxSkillSize {
		return protocol.InvalidParams(fmt.Errorf("skill %s is over %d MiB", s.Name, maxSkillSize>>20))
	}
	return nil
}

// localPath reports whether p stays inside the folder it is relative to.
func localPath(p string) bool {
	return filepath.IsLocal(p) && !strings.Contains(p, `\`)
}

func isText(b []byte) bool {
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0)
}

// sniffSize is how much of a zip entry decides whether it is text.
const sniffSize = 8 << 10

// headIsText reports whether h, the start of a file, may be text: no NUL,
// and valid UTF-8 apart from a rune cut off at the end.
func headIsText(h []byte) bool {
	if bytes.IndexByte(h, 0) >= 0 {
		return false
	}
	for k := 0; k < utf8.UTFMax && k <= len(h); k++ {
		if utf8.Valid(h[:len(h)-k]) && (k == 0 || !utf8.FullRune(h[len(h)-k:])) {
			return true
		}
	}
	return false
}

// parseSkillMarkdown reads a SKILL.md: YAML frontmatter between --- lines,
// then the Markdown body.
func parseSkillMarkdown(b []byte) (skill, error) {
	text := strings.ReplaceAll(strings.TrimPrefix(string(b), "\uFEFF"), "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		if strings.HasPrefix(strings.TrimSpace(text), "<") {
			return skill{}, protocol.InvalidParams(errors.New("got a web page, not a SKILL.md or a zip; link the raw file"))
		}
		return skill{}, protocol.InvalidParams(errors.New("a SKILL.md starts with --- and YAML frontmatter"))
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		front, ok = strings.CutSuffix(strings.TrimRight(rest, "\n"), "\n---")
	}
	if !ok {
		return skill{}, protocol.InvalidParams(errors.New("the SKILL.md frontmatter has no closing ---"))
	}
	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
		return skill{}, protocol.InvalidParams(fmt.Errorf("SKILL.md frontmatter: %v", err))
	}
	s := skill{Name: strings.TrimSpace(meta.Name), Description: strings.TrimSpace(meta.Description), Body: strings.TrimSpace(body), Enabled: true}
	return s, s.check()
}

// parseUpload reads a SKILL.md or a zip of a skill folder, told apart by the
// zip signature rather than a file name, which a URL may not have.
func parseUpload(b []byte) (skill, error) {
	if bytes.HasPrefix(b, []byte("PK\x03\x04")) {
		return parseZip(b)
	}
	if len(b) > maxFileSize {
		return skill{}, protocol.InvalidParams(fmt.Errorf("a SKILL.md must be at most %d MiB", maxFileSize>>20))
	}
	return parseSkillMarkdown(b)
}

// parseZip reads a skill folder from a zip holding exactly one SKILL.md. The
// folder holding it is the skill's root; files elsewhere and files that
// aren't text, such as images, are left out.
func parseZip(b []byte) (skill, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return skill{}, protocol.InvalidParams(fmt.Errorf("not a readable zip: %v", err))
	}
	if len(zr.File) > maxZipEntries {
		return skill{}, protocol.InvalidParams(fmt.Errorf("the zip has %d entries; the limit is %d", len(zr.File), maxZipEntries))
	}
	var main *zip.File
	for _, f := range zr.File {
		// Nothing is extracted to disk, but a path that escapes the folder
		// means a malicious or broken archive.
		if !localPath(f.Name) {
			return skill{}, protocol.InvalidParams(fmt.Errorf("the zip's entry %q leaves its folder", f.Name))
		}
		if path.Base(f.Name) == "SKILL.md" && !f.FileInfo().IsDir() {
			if main != nil {
				return skill{}, protocol.InvalidParams(errors.New("the zip holds more than one SKILL.md; import one skill per zip"))
			}
			main = f
		}
	}
	if main == nil {
		return skill{}, protocol.InvalidParams(errors.New("the zip holds no SKILL.md"))
	}
	total := 0
	// read returns f's text, or nil for a file that isn't text. Only text
	// counts toward the limits, so a large image doesn't fail the import.
	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("%s: %v", f.Name, err))
		}
		defer rc.Close()
		head := make([]byte, sniffSize)
		n, err := io.ReadFull(rc, head)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, protocol.InvalidParams(fmt.Errorf("%s: %v", f.Name, err))
		}
		if !headIsText(head[:n]) {
			return nil, nil
		}
		// The header's size can lie, so the reader is what's limited.
		rest, err := io.ReadAll(io.LimitReader(rc, int64(maxFileSize+1-n)))
		if err != nil {
			return nil, protocol.InvalidParams(fmt.Errorf("%s: %v", f.Name, err))
		}
		b := append(head[:n], rest...)
		if len(b) > maxFileSize {
			return nil, protocol.InvalidParams(fmt.Errorf("%s is over %d MiB", f.Name, maxFileSize>>20))
		}
		if !isText(b) {
			return nil, nil
		}
		if total += len(b); total > maxSkillSize {
			return nil, protocol.InvalidParams(fmt.Errorf("the skill is over %d MiB", maxSkillSize>>20))
		}
		return b, nil
	}
	mb, err := read(main)
	if err != nil {
		return skill{}, err
	}
	if mb == nil {
		return skill{}, protocol.InvalidParams(fmt.Errorf("%s must be UTF-8 text without NUL characters", main.Name))
	}
	s, err := parseSkillMarkdown(mb)
	if err != nil {
		return skill{}, err
	}
	root := path.Dir(main.Name)
	for _, f := range zr.File {
		rel := strings.TrimPrefix(f.Name, root+"/")
		if f == main || f.FileInfo().IsDir() || (root != "." && rel == f.Name) {
			continue
		}
		fb, err := read(f)
		if err != nil {
			return skill{}, err
		}
		if fb == nil {
			continue
		}
		if s.Files == nil {
			s.Files = map[string]string{}
		}
		s.Files[path.Clean(rel)] = string(fb)
	}
	return s, s.check()
}

// fetchSkill downloads a SKILL.md or a zip. Private and loopback addresses
// are allowed, since skills may live on the owner's LAN; importing needs
// write access in the core.
func fetchSkill(ctx context.Context, raw string) (skill, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return skill{}, protocol.InvalidParams(fmt.Errorf("%q isn't an http or https URL", raw))
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return skill{}, protocol.InvalidParams(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return skill{}, protocol.InvalidParams(fmt.Errorf("fetch %s: %v", raw, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return skill{}, protocol.InvalidParams(fmt.Errorf("fetch %s: %s", raw, resp.Status))
	}
	b, err := readUpload(resp.Body)
	if err != nil {
		return skill{}, err
	}
	return parseUpload(b)
}

// readUpload reads r, refusing more than maxUpload bytes.
func readUpload(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxUpload+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(b) > maxUpload {
		return nil, protocol.InvalidParams(fmt.Errorf("the file is over %d MiB", maxUpload>>20))
	}
	return b, nil
}
