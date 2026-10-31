package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// premiseKey and themesKey hold a card's premise and themes, which core
// embeds in place of the card's text so similar cards share a premise, not wording.
const premiseKey, themesKey = "premise", "themes"

// summaryChars caps the card text sent, leaving room in a 16k-token context.
const summaryChars = 36000

// summaryFields are the card fields that say what the card is about.
var summaryFields = []string{"description", "personality", "scenario", "system_prompt", "depth_prompt", "first_mes"}

var summaryLine = regexp.MustCompile(`(?im)^\**(premise|themes)\**:?\**:?[ \t]*(.*)$`)

//go:embed prompts/summary.tmpl
var summaryPrompt string

// promptVersion names the summary prompt in a vector's space, so vectors
// from an edited prompt aren't compared with older ones.
var promptVersion = func() string {
	h := sha256.Sum256([]byte(summaryPrompt))
	return hex.EncodeToString(h[:4])
}()

// summarize asks the summary connection for a revision's premise and themes,
// at temperature 0 so a card always gets the same summary.
func summarize(ctx context.Context, conn *protocol.Conn, s *settings, data json.RawMessage) (map[string]json.RawMessage, error) {
	text, err := summaryText(data)
	if err != nil {
		return nil, err
	}
	temperature, maxTokens := 0.0, 512
	var res protocol.AIChatResult
	err = conn.Call(ctx, protocol.MethodAIChat, protocol.AIChatParams{
		ServiceCall: protocol.ServiceCall{Instance: s.Summary},
		Messages:    []protocol.ChatMessage{{Role: "user", Content: summaryPrompt + text}},
		Temperature: &temperature, MaxTokens: &maxTokens,
	}, &res)
	if err != nil {
		return nil, fmt.Errorf("summarize: %w", err)
	}
	return summaryValues(res.Text)
}

// summaryText is the card's name and the fields in summaryFields, each under
// its own heading.
func summaryText(data json.RawMessage) (string, error) {
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		return "", fmt.Errorf("read card: %w", err)
	}
	if ext, ok := card["extensions"].(map[string]any); ok {
		if dp, ok := ext["depth_prompt"].(map[string]any); ok {
			card["depth_prompt"] = dp["prompt"]
		}
	}
	parts := []string{fmt.Sprintf("Name: %v", card["name"])}
	for _, f := range summaryFields {
		if v, _ := card[f].(string); strings.TrimSpace(v) != "" {
			parts = append(parts, "## "+f+"\n"+strings.TrimSpace(v))
		}
	}
	text := strings.Join(parts, "\n\n")
	if len(text) > summaryChars {
		text = strings.ToValidUTF8(text[:summaryChars], "")
	}
	return text, nil
}

// summaryValues reads the premise and themes lines of a model's reply.
func summaryValues(reply string) (map[string]json.RawMessage, error) {
	var premise string
	var themes []string
	for _, m := range summaryLine.FindAllStringSubmatch(reply, -1) {
		v := strings.TrimSpace(m[2])
		switch strings.ToLower(m[1]) {
		case premiseKey:
			premise = v
		case themesKey:
			for t := range strings.SplitSeq(v, ",") {
				if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !slices.Contains(themes, t) {
					themes = append(themes, t)
				}
			}
		}
	}
	if premise == "" || len(themes) == 0 {
		return nil, errors.New("the model's summary has no premise or themes line")
	}
	p, _ := json.Marshal(premise)
	t, _ := json.Marshal(themes)
	return map[string]json.RawMessage{premiseKey: p, themesKey: t}, nil
}
