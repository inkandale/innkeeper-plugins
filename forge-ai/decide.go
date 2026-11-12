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

// premiseKey rates the card's idea apart from its writing, so quality, a
// measure of the writing, leaves it out.
const premiseKey = "premise"

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
	{"consistency", "How well do the fields agree with each other?", []string{
		"Fields describe different characters or premises",
		"Several conflicts a player would notice, such as a greeting that acts against the personality",
		"One conflict a player would notice",
		"Only minor slips that wouldn't change play",
		"Every field agrees, and the greeting and examples show the personality as described",
	}},
	{premiseKey, "Judging the idea rather than the writing, how worth playing is the concept?", []string{
		"No discernible concept",
		"A character type with no situation or hook",
		"A familiar trope played straight, with little tension",
		"A specific concept with some tension, or a familiar trope with a fresh angle",
		"A specific, unusual concept with built-in tension and many directions to go",
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
// level scaled to 0 to 100, and quality the mean of all but premise, as in
// the chat model's evaluation.
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
	inQuality := float64(len(decideQuestions) - 1) // every question but premise
	for _, q := range decideQuestions {
		a, ok := res.Answers[q.key]
		if !ok || a.Score == nil {
			return nil, valueMeta{}, fmt.Errorf("the decision model didn't score %s", q.key)
		}
		score := min(max(*a.Score/float64(len(q.levels)-1)*100, 0), 100)
		if q.key != premiseKey {
			quality += score / inQuality
		}
		if a.Confidence != nil {
			confidence += *a.Confidence / float64(len(decideQuestions))
		}
		values[q.key], _ = json.Marshal(score)
	}
	if confidence < in.settings.escalateBelow() {
		return nil, valueMeta{}, errUnsure
	}
	values[qualityKey], _ = json.Marshal(quality)
	return values, valueMeta{Model: res.Model, Prompt: decidePrompt()}, nil
}
