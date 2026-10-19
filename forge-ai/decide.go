package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/protocol"
)

// decideQuestion is one score question for a decision model. Levels describe
// situations, low to high, since a decision model matches the state to them.
type decideQuestion struct {
	key          string
	instructions string
	levels       []string
}

// craftKey feeds quality but isn't stored: the chat model's evaluation has no
// matching dimension.
const craftKey = "craft"

var decideQuestions = []decideQuestion{
	{"first_impression", "How well does the greeting (first message) open a roleplay?", []string{
		"No greeting, or one or two flat lines with no scene",
		"A greeting that states the setup but gives little scene or reason to reply",
		"A clear scene and a line to answer, with ordinary writing",
		"A vivid scene, a distinct opening line, and an obvious way for the user to respond",
		"An excellent hook: vivid, specific, in character, and it invites a reply without deciding for the user",
	}},
	{"voice", "How distinct and consistent is the character's voice in the greeting and examples?", []string{
		"No dialogue, or dialogue anyone could have said",
		"Some personality, but generic phrasing",
		"A recognizable voice in places",
		"A distinct voice that matches the description throughout",
		"An unmistakable voice: word choice, rhythm, and attitude are all the character's own",
	}},
	{"roleplayability", "How much does the card give a roleplay to work with?", []string{
		"Nothing to do: no scenario, goal, or situation",
		"A premise but no situation to start from",
		"A situation to start from, with little room to develop",
		"A clear situation with hooks and room to develop",
		"Rich: a situation, tensions, and several directions the roleplay could go",
	}},
	{craftKey, "How well written is the card overall?", []string{
		"Mostly empty, broken, or unreadable",
		"Rough: typos, keyword lists in place of prose, or contradictions",
		"Readable and serviceable",
		"Well written and organized",
		"Polished: clear, specific, and economical",
	}},
}

// defaultEscalateBelow re-scores a card with the chat model when the decision
// model's mean confidence is under it.
const defaultEscalateBelow = 0.25

// decideRequest is the questions as ai.decide sends them.
func decideRequest() map[string]protocol.DecideQuestion {
	qs := map[string]protocol.DecideQuestion{}
	for _, q := range decideQuestions {
		levels, _ := json.Marshal(q.levels)
		qs[q.key] = protocol.DecideQuestion{Type: "score", Instructions: q.instructions, Criteria: levels}
	}
	return qs
}

// decidePrompt names the questions in each value's meta, as evaluatePrompt
// names the evaluate setup.
func decidePrompt() string {
	b, _ := json.Marshal(decideRequest())
	sum := sha256.Sum256(b)
	return "decide-" + hex.EncodeToString(sum[:4])
}

// decideState is the card fields the questions are about. Fewer tokens is
// faster: some servers read the state once per question.
func decideState(data json.RawMessage) (json.RawMessage, error) {
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, err
	}
	state := map[string]any{}
	for _, k := range []string{"name", "description", "personality", "scenario", "first_mes", "mes_example"} {
		if v, ok := card[k]; ok && v != "" {
			state[k] = v
		}
	}
	return json.Marshal(state)
}

// errUnsure is a decision too uncertain to store; the chat model scores the card.
var errUnsure = errors.New("the decision model isn't confident enough")

// decideScores scores one revision with the decision model: each question's
// level scaled to 0 to 100, and quality their mean.
func (srv *server) decideScores(ctx context.Context, in *instance, rev protocol.EnrichRevision) (map[string]json.RawMessage, valueMeta, error) {
	state, err := decideState(rev.Data)
	if err != nil {
		return nil, valueMeta{}, err
	}
	var res protocol.AIDecideResult
	err = in.conn.Call(ctx, protocol.MethodAIDecide, protocol.AIDecideParams{ServiceCall: protocol.ServiceCall{Instance: in.settings.Decide},
		State: state, Questions: decideRequest()}, &res)
	if err != nil {
		return nil, valueMeta{}, err
	}
	values := map[string]json.RawMessage{}
	var quality, confidence float64
	for _, q := range decideQuestions {
		a, ok := res.Answers[q.key]
		if !ok || a.Score == nil {
			return nil, valueMeta{}, fmt.Errorf("the decision model didn't score %s", q.key)
		}
		score := min(max(*a.Score/float64(len(q.levels)-1)*100, 0), 100)
		quality += score / float64(len(decideQuestions))
		if a.Confidence != nil {
			confidence += *a.Confidence / float64(len(decideQuestions))
		}
		if q.key != craftKey {
			values[q.key], _ = json.Marshal(score)
		}
	}
	if confidence < in.settings.escalateBelow() {
		return nil, valueMeta{}, errUnsure
	}
	values[qualityKey], _ = json.Marshal(quality)
	return values, valueMeta{Model: res.Model, Prompt: decidePrompt()}, nil
}
