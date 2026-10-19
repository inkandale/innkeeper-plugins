package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
	anyllm "github.com/mozilla-ai/any-llm-go"
)

// chatConfidence scales a chat model's confidence: its stated probabilities
// are guesses, not a decision model's read-out.
const chatConfidence = 0.5

// decide answers ai.decide through the server's /v1/systemone when the
// connection ticks decide, and through its chat model otherwise.
func (c *connection) decide(ctx context.Context, params json.RawMessage) (any, error) {
	if err := c.offering(protocol.MethodAIDecide); err != nil {
		return nil, err
	}
	var p protocol.AIDecideParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return nil, protocol.InvalidParams(errors.New("ai.decide needs questions"))
	}
	if slices.Contains(c.settings.Kinds, "decide") {
		return systemOne(ctx, c, p)
	}
	return decideByChat(ctx, c, p)
}

// systemOne sends the questions to the server's /v1/systemone, whose
// request and reply ai.decide mirrors.
func systemOne(ctx context.Context, c *connection, p protocol.AIDecideParams) (protocol.AIDecideResult, error) {
	var res protocol.AIDecideResult
	req := map[string]any{"model": c.settings.Model, "state": p.State, "questions": p.Questions}
	if len(p.Images) > 0 {
		req["images"] = p.Images
	}
	body, err := json.Marshal(req)
	if err != nil {
		return res, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.settings.BaseURL, "/")+"/systemone", bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return res, protocol.NewError(protocol.CodeInternalError, hideKey(err.Error(), c.apiKey), true)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		text := hideKey(fmt.Sprintf("systemone: %s: %s", resp.Status, bytes.TrimSpace(msg)), c.apiKey)
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		code := protocol.CodeAIRejected
		if retry {
			code = protocol.CodeInternalError
		}
		return res, protocol.NewError(code, text, retry)
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return res, protocol.NewError(protocol.CodeInternalError, "systemone: unreadable reply: "+err.Error(), true)
	}
	// Sorted so the error names the same missing answer every time.
	for _, id := range slices.Sorted(maps.Keys(p.Questions)) {
		if _, ok := res.Answers[id]; !ok {
			return res, protocol.NewError(protocol.CodeInternalError, "systemone: no answer for "+id, true)
		}
	}
	if res.Model == "" {
		res.Model = c.settings.Model
	}
	return res, nil
}

// decideByChat asks the chat model for each question's probabilities and
// shapes them as a decision model's answers.
func decideByChat(ctx context.Context, c *connection, p protocol.AIDecideParams) (protocol.AIDecideResult, error) {
	res := protocol.AIDecideResult{Answers: map[string]protocol.DecideAnswer{}, Model: c.settings.Model}
	options := map[string][]string{}
	for id, q := range p.Questions {
		names, err := optionNames(q)
		if err != nil {
			return res, protocol.InvalidParams(fmt.Errorf("%s: %v", id, err))
		}
		options[id] = names
	}
	zero := 0.0
	text, _, err := stream(ctx, c, anyllm.CompletionParams{Model: c.settings.Model, Temperature: &zero,
		Messages: []anyllm.Message{{Role: anyllm.RoleUser, Content: decidePrompt(p)}}}, func(string) {})
	if err != nil {
		return res, modelError(err)
	}
	reply, err := jsonObject(text)
	if err != nil {
		return res, unreadable(err)
	}
	for id, q := range p.Questions {
		raw, ok := reply[id]
		if !ok {
			return res, unreadable(fmt.Errorf("no answer for %s", id))
		}
		a, err := chatAnswer(q.Type, options[id], raw)
		if err != nil {
			return res, unreadable(fmt.Errorf("%s: %w", id, err))
		}
		res.Answers[id] = a
	}
	return res, nil
}

// unreadable is a chat reply that isn't the JSON asked for; another try may do.
func unreadable(err error) error {
	return protocol.NewError(protocol.CodeInternalError, "the model's reply wasn't the JSON asked for: "+err.Error(), true)
}

// optionNames are a question's option names, or its level numbers as strings.
func optionNames(q protocol.DecideQuestion) ([]string, error) {
	switch q.Type {
	case "choice":
		var opts map[string]json.RawMessage
		if json.Unmarshal(q.Criteria, &opts) != nil || len(opts) < 2 {
			return nil, fmt.Errorf("a choice needs criteria naming at least two options")
		}
		return slices.Sorted(maps.Keys(opts)), nil
	case "score":
		var levels []json.RawMessage
		if json.Unmarshal(q.Criteria, &levels) != nil || len(levels) < 2 || len(levels) > 10 {
			return nil, fmt.Errorf("a score needs 2 to 10 levels")
		}
		names := make([]string, len(levels))
		for i := range levels {
			names[i] = strconv.Itoa(i)
		}
		return names, nil
	case "noul":
		return nil, nil
	}
	return nil, fmt.Errorf("type %q isn't choice, score, or noul", q.Type)
}

func decidePrompt(p protocol.AIDecideParams) string {
	var b strings.Builder
	b.WriteString("Answer each question about the state below on its own. Reply with only a JSON object with one key per question ID:\n" +
		"- choice: an object giving the probability of each option, by name.\n" +
		"- score: an object giving the probability of each level, by number.\n" +
		"- noul: the probability, from 0 to 1, that the statement is true.\n\nQuestions:\n")
	for _, id := range slices.Sorted(maps.Keys(p.Questions)) {
		q := p.Questions[id]
		fmt.Fprintf(&b, "\n%s (%s): %s\n", id, q.Type, q.Instructions)
		switch q.Type {
		case "choice":
			var opts map[string]json.RawMessage
			_ = json.Unmarshal(q.Criteria, &opts)
			for _, name := range slices.Sorted(maps.Keys(opts)) {
				fmt.Fprintf(&b, "  %s: %s\n", name, describe(opts[name]))
			}
		case "score":
			var levels []json.RawMessage
			_ = json.Unmarshal(q.Criteria, &levels)
			for i, l := range levels {
				fmt.Fprintf(&b, "  %d: %s\n", i, describe(l))
			}
		}
	}
	b.WriteString("\nState:\n")
	var s string
	if json.Unmarshal(p.State, &s) == nil {
		b.WriteString(s)
	} else {
		b.Write(p.State)
	}
	return b.String()
}

// describe is a criterion as text: a string as is, anything richer as JSON.
func describe(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// jsonObject reads the JSON object in text, which may be wrapped in prose
// or a code fence.
func jsonObject(text string) (map[string]json.RawMessage, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object")
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// chatAnswer turns a chat model's probabilities into an answer.
func chatAnswer(kind string, names []string, raw json.RawMessage) (protocol.DecideAnswer, error) {
	a := protocol.DecideAnswer{Type: kind}
	if kind == "noul" {
		var p float64
		if err := json.Unmarshal(raw, &p); err != nil || p < 0 || p > 1 {
			return a, fmt.Errorf("want a probability from 0 to 1")
		}
		a.Noul = &p
		return a, nil
	}
	var stated map[string]float64
	if err := json.Unmarshal(raw, &stated); err != nil {
		return a, fmt.Errorf("want probabilities by option")
	}
	probs := make([]float64, len(names))
	var sum float64
	for i, name := range names {
		if stated[name] < 0 {
			return a, fmt.Errorf("negative probability for %s", name)
		}
		probs[i] = stated[name]
		sum += probs[i]
	}
	if sum == 0 {
		return a, fmt.Errorf("no probability on any option")
	}
	a.Probabilities = map[string]float64{}
	best := 0
	for i := range probs {
		probs[i] /= sum
		a.Probabilities[names[i]] = probs[i]
		if probs[i] > probs[best] {
			best = i
		}
	}
	var conf float64
	if kind == "choice" {
		a.Choice = names[best]
		conf = choiceConfidence(probs[best], len(probs))
	} else {
		var score float64
		for i, p := range probs {
			score += float64(i) * p
		}
		a.Score = &score
		conf = scoreConfidence(probs, best)
	}
	conf *= chatConfidence
	a.Confidence = &conf
	return a, nil
}

// choiceConfidence is 1 with all probability on the top option and 0 when
// it is spread evenly.
func choiceConfidence(top float64, n int) float64 {
	even := 1 / float64(n)
	return (top - even) / (1 - even)
}

// scoreConfidence is 1 with all probability on the likeliest level, falling
// with the expected distance from it, to 0 at the distance an even spread
// gives. A neighboring level costs less than a far one.
func scoreConfidence(probs []float64, best int) float64 {
	var dist, even float64
	for i, p := range probs {
		d := math.Abs(float64(i - best))
		dist += p * d
		even += d / float64(len(probs))
	}
	return max(0, 1-dist/even)
}

// probeDecide asks one yes-or-no question.
func probeDecide(ctx context.Context, c *connection) error {
	_, err := systemOne(ctx, c, protocol.AIDecideParams{State: json.RawMessage(`"Hello."`),
		Questions: map[string]protocol.DecideQuestion{"greeting": {Type: "noul", Instructions: "The state is a greeting."}}})
	return err
}
