# Reforge skills implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ten Agent Skills that make forge-ai's Reforge improve a card's
craft and steer its story, and a fix so a whole-card Reforge plans with the
skills the owner mentions.

**Architecture:** The skills are Markdown folders in `forge-ai/skills/`,
imported through the existing skill importer. One Go change, in
`forge-ai/wholecard.go`, adds the mentioned skills to the whole-card plan
call. A Go test checks that every skill folder imports. Design:
[2026-11-13-reforge-skills-design.md](2026-11-13-reforge-skills-design.md).

**Tech stack:** Go 1.x (forge-ai plugin), Agent Skills `SKILL.md` format,
PostgreSQL for the skill tests.

---

## Before you start

- Work in `~/git/innkeeper-plugins-reforge-skills` (branch `reforge-skills`).
  Never check out another branch in it, and never touch
  `~/git/innkeeper-plugins`.
- Skill tests need the database, and skip silently without it. Export this in
  every shell that runs tests:

  ```bash
  export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
  export INNKEEPER_PLUGIN_DATABASE_URL=$INNKEEPER_TEST_DATABASE_URL
  ```

- `TestEnrichCancelStopsFurtherCalls` fails on main regardless of changes.
  Ignore that one failure.
- Read `AGENTS.md` in `~/git/innkeeper` (it applies to plugins too) and the
  `documentation-writing` rules before writing prose.

## File map

| File | Change |
|---|---|
| `forge-ai/wholecard.go` | `plan` appends the mentioned skills to its system prompt |
| `forge-ai/wholecard_test.go` | Test for the above |
| `forge-ai/skills_test.go` | New: every folder in `skills/` imports through `parseZip` with a matching name |
| `forge-ai/skills/<name>/SKILL.md` and `references/*.md` | New: ten skills |
| `forge-ai/README.md` | Plan-call sentence; how to import the bundled skills |

---

### Task 1: Whole-card plan sees mentioned skills

**Files:**
- Modify: `forge-ai/wholecard.go` (`plan`, around line 58)
- Test: `forge-ai/wholecard_test.go`

- [ ] **Step 1: Write the failing test**

Append to `forge-ai/wholecard_test.go`:

```go
func TestWholeCardPlanGetsForcedSkills(t *testing.T) {
	llm, h := skillsHost(t,
		streamed(`{"description": "Cut the repeated height.", "personality": "skip"}`),
		streamed(`{"description": "A towering orc."}`))
	p := sampleRun(t, "tighten")
	p.Field = ""
	p.Context.Skills = []string{"college"}
	if _, err := h.run(t, p); err != nil {
		t.Fatal(err)
	}
	plan := llm.requests[0]
	if !strings.Contains(systemPrompt(plan), "### college\n\n# College") {
		t.Fatalf("plan system prompt %q", systemPrompt(plan))
	}
	if plan["tools"] != nil {
		t.Fatalf("plan offered tools %v", plan["tools"])
	}
}

func TestWholeCardUnknownSkillRefusedBeforePlanning(t *testing.T) {
	llm, h := skillsHost(t)
	p := sampleRun(t, "tighten")
	p.Field = ""
	p.Context.Skills = []string{"nope"}
	_, err := h.run(t, p)
	wantInvalid(t, err, "unknown skill")
	if len(llm.requests) != 0 {
		t.Fatalf("%d requests sent before refusing", len(llm.requests))
	}
}
```

- [ ] **Step 2: Run the tests to check they fail**

```bash
cd ~/git/innkeeper-plugins-reforge-skills
go test ./forge-ai -run 'TestWholeCardPlanGetsForcedSkills|TestWholeCardUnknownSkillRefusedBeforePlanning' -v
```

Expected: `TestWholeCardPlanGetsForcedSkills` fails with `plan system prompt ...`
(no `### college`). `TestWholeCardUnknownSkillRefusedBeforePlanning` fails
with `1 requests sent before refusing`, because the plan call goes out
before the first field step refuses. If either says `SKIP`, the database
variable isn't set.

- [ ] **Step 3: Implement**

In `forge-ai/wholecard.go`, in `plan`, replace:

```go
	system, user, err := r.planPrompts(in.settings)
	if err != nil {
		return nil, "", err
	}
```

with:

```go
	system, user, err := r.planPrompts(in.settings)
	if err != nil {
		return nil, "", err
	}
	if len(r.Context.Skills) > 0 {
		enabled, err := in.enabledSkills(ctx)
		if err != nil {
			return nil, "", fmt.Errorf("list skills: %w", err)
		}
		forced, err := in.forcedSkills(ctx, enabled, r.Context.Skills)
		if err != nil {
			return nil, "", err
		}
		system += skillsPrompt(forced)
	}
```

`fmt` is already imported in `wholecard.go`. `wholeCard` wraps the error as
`plan: ...`, and `protocol.InvalidParams` survives the `%w` wrap, so
`wantInvalid` still matches.

- [ ] **Step 4: Run the tests to check they pass**

```bash
go test ./forge-ai -run 'TestWholeCard|TestForcedSkill|TestNoToolsKeepsForcedSkills' -v
```

Expected: all PASS.

- [ ] **Step 5: Update the README**

In `forge-ai/README.md`, find:

```
library through `library.call` when the owner turns on Library access for
the instance. A whole-card run's plan call offers no tools; each field's
call does.
```

Replace the second sentence so the paragraph reads:

```
library through `library.call` when the owner turns on Library access for
the instance. A whole-card run's plan call offers no tools, but it gets
the mentioned skills' instructions; each field's call offers tools.
```

Then in the `## Skills` bullet list, after the bullet that begins
``- A `@skills/<name>` mention``, check that nothing contradicts this. That
bullet says the mention puts the instructions "in the system prompt", which
now covers the plan call too, so leave it.

- [ ] **Step 6: Run the whole plugin suite**

```bash
go vet ./forge-ai && go test -race ./forge-ai
```

Expected: PASS, except `TestEnrichCancelStopsFurtherCalls`.

- [ ] **Step 7: Commit**

```bash
git add forge-ai/wholecard.go forge-ai/wholecard_test.go forge-ai/README.md
git commit -m "fix(forge-ai): plan a whole-card edit with the mentioned skills"
```

---

### Task 2: Skill import check

Every folder in `forge-ai/skills/` must import through the real importer.
The test zips each folder the way an owner would and runs `parseZip`.

**Files:**
- Create: `forge-ai/skills_test.go`

- [ ] **Step 1: Write the test**

```go
package main

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// zipFolder zips dir as an owner would: the folder itself is the zip's top
// entry, so SKILL.md sits at <name>/SKILL.md.
func zipFolder(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parent := filepath.Dir(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(parent, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBundledSkillsImport(t *testing.T) {
	entries, err := os.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no skills in skills/")
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("skills/%s: only skill folders belong in skills/", e.Name())
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			sk, err := parseZip(zipFolder(t, filepath.Join("skills", e.Name())))
			if err != nil {
				t.Fatal(err)
			}
			if sk.Name != e.Name() {
				t.Fatalf("frontmatter name %q, folder %q", sk.Name, e.Name())
			}
		})
	}
}
```

This test needs no database.

- [ ] **Step 2: Run it to check it fails**

```bash
go test ./forge-ai -run TestBundledSkillsImport -v
```

Expected: FAIL, `open skills: no such file or directory`. It passes once
Task 3 adds the first skill. Don't commit it alone; it's committed with
Task 3.

---

## Writing a skill (applies to Tasks 3 and 5 to 13)

Every skill follows the same layout. A task names the skill, its exact
frontmatter, its sources, and what it must contain; this section is how to
write it.

**Folder:** `forge-ai/skills/<name>/SKILL.md`, plus `references/<file>.md`
when the task lists one. No other files.

**`SKILL.md` shape:**

```markdown
---
name: <name>
description: "<exact text from the task>"
---

# <Title>

<One or two sentences: what this skill changes in a card.>

## Protect

- <What the edit keeps.>

## Failure modes

- <How a model gets this wrong, each with a short bad/better example.>

## Edits per field

| Field | Edit |
|---|---|
| `description` | ... |

## Done when

- <A check a reader can verify on the edited card.>

## Always

- Never write `{{user}}`'s actions, words, or thoughts, in any field.
- State rules as what to do. Naming a behavior to forbid it steers the model toward it.
- Never compress or cut example dialogue to save tokens; it sets the character's voice.
- Bring `creator_notes` in line with the edit; keep its credits and links.

<Credits line, when the task names one.>
```

**Rules:**

- Keep the description in double quotes: several contain `: `, which
  breaks an unquoted YAML value.
- The `## Always` block is identical in every skill. Copy it exactly.
- The body is 150 to 300 lines. Detailed checklists and before/after
  examples go in `references/`, and the body names each file ("For the full
  checklist, load `references/checklist.md`.").
- Write for the model doing a Reforge: it is editing an existing card's
  fields, not writing a scene or a whole new card. Every instruction says
  which field changes and how.
- Use the card field names exactly: `description`, `personality`,
  `scenario`, `first_mes`, `mes_example`, `system_prompt`,
  `post_history_instructions`, `creator_notes`, `alternate_greetings`, and
  "lorebook entries".
- Lorebook advice uses only what `revise-lorebook` can set: `comment`,
  `keys`, `secondary_keys`, `content`, `constant`. No delay, sticky,
  cooldown, or probability.
- All text is original. Ideas from talemate (AGPL), Marinara (no license),
  Chub cards, and guides are paraphrased; no sentence is copied. Text from
  `~/git/sillytavern-skills` is the owner's and may be adapted directly.
- Use nothing from TaleScribe (142156), Character Generator (111756, 98475,
  65412), or Character Bot Creator Helper (246287).
- Examples in the skill use adult characters only.

**Verify each skill:**

```bash
go test ./forge-ai -run TestBundledSkillsImport -v
wc -l forge-ai/skills/<name>/SKILL.md          # 150 to 300
grep -c 'Never write `{{user}}`' forge-ai/skills/<name>/SKILL.md   # 1
```

**Research notes** referenced below are in
`docs/plans/2026-11-13-reforge-skills-research/`: `local.md`, `web.md`,
`chub.md`. Read the named sections before writing.

---

### Task 3: `card-craft`

**Files:**
- Create: `forge-ai/skills/card-craft/SKILL.md`
- Create: `forge-ai/skills/card-craft/references/fields.md`
- Create: `forge-ai/skills/card-craft/references/examples.md`
- Create: `forge-ai/skills/card-craft/references/unslop.md`
- Commit with: `forge-ai/skills_test.go` from Task 2

**Frontmatter description (exact):**
`Improve a character card's craft: traits that cause observable behavior, a character who fails and resists, example dialogue that trains the right voice and length, and prose without filler. Use for any Reforge that asks to improve, fix, strengthen, or clean up a card.`

**Sources:**
- `~/git/sillytavern-skills/skills/character-card-v3-generator/references/character-writing-guidelines.md`
  (causal construction, testable traits) and `reviewing-cards.md`.
- `local.md`: "Concrete techniques in the skill, by file", "forge-ai: has vs missing".
- `web.md`: 1a, 1b, 2a, 2c, 2d, 2f, 2g, 2i, 7.
- `chub.md`: 1 (Delphi), 4 (WW+), 5 (Adeline), "Utility cards" (Token Reducer).
- talemate `editor/unslop.jinja2` (paraphrase only).
- Read `forge-ai/prompts/shared.tmpl` and `field.tmpl` first; don't repeat
  field ownership the prompts already give. Add what they lack.

**Body must contain:**
- Causal construction: trait → cause → observable behavior; a shared cause
  stated in `personality` and shown in `scenario` and `mes_example`; one
  specific unresolved memory instead of a full backstory; at least one
  unresolved tension.
- Testable traits: each trait passes "would a reader know it was violated?"
  Bad/better example.
- How the character fails and handles conflict; dislikes stated outright so
  the character doesn't drift agreeable; tiered preferences (strongly vs
  mildly).
- "Does this line ever change a reply? If not, cut it."
- A physical tell tied to a secret.
- Example dialogue: a short `{{user}}` line followed by the character's
  reaction (never `{{user}}`'s actions narrated); varied verbs across
  examples; example length sets reply length.
- The current situation belongs in `first_mes`, not `scenario`.
- Edit discipline: change only what the instruction asks; keep the card's
  format, point of view, and style.

**References:**
- `fields.md`: what each field does at runtime (sent every turn, sent once,
  dropped as context fills, never sent), from `web.md` 2a and 2b.
- `examples.md`: three before/after `mes_example` rewrites.
- `unslop.md`: purple prose, robotic dialogue, over-description, announced
  emotion, each with a before/after in original wording.

- [ ] **Step 1:** Read the sources listed above.
- [ ] **Step 2:** Write `SKILL.md` and the three reference files.
- [ ] **Step 3:** Run the verify commands from "Writing a skill". Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills_test.go forge-ai/skills/card-craft
git commit -m "feat(forge-ai): add card-craft skill and skill import check"
```

---

### Task 4: Trial `card-craft` (checkpoint with the owner)

The owner runs this, or approves running it. Stop after it and report;
Tasks 5 to 13 start only if the owner says the skill helps.

- [ ] **Step 1: Build the import zip**

```bash
cd ~/git/innkeeper-plugins-reforge-skills/forge-ai/skills
zip -r /tmp/card-craft.zip card-craft
```

- [ ] **Step 2:** Deploy this branch's forge-ai to the owner's Innkeeper, or
  run it locally, and import `/tmp/card-craft.zip` on the forge-ai
  instance's settings page under Skills.
- [ ] **Step 3:** Pick six cards in the owner's library whose `quality`
  score is between 40 and 70.
- [ ] **Step 4:** For each card, run a whole-card Reforge with the
  instruction `Improve this card.` twice: once plain, once with
  `@skills/card-craft Improve this card.` Don't apply either proposal; read
  them.
- [ ] **Step 5:** Run Evaluate on each proposal (apply it to a fork, then
  evaluate), and record a table: card, baseline quality, plain-Reforge
  quality, skill-Reforge quality, and one line on what the skill changed.
- [ ] **Step 6:** Report the table to the owner and wait. If the skill
  doesn't help, revise `card-craft` and repeat before going on.

---

### Task 5: `story-engine`

**Files:**
- Create: `forge-ai/skills/story-engine/SKILL.md`
- Create: `forge-ai/skills/story-engine/references/gm-moves.md`

**Frontmatter description (exact):**
`Make a card drive the story instead of waiting: each reply makes one move, then hands {{user}} a choice; every action {{user}} takes is an attempt that can fail or succeed at a cost. Use when the owner asks for a card that pushes the plot, runs scenes, or acts as a narrator or game master.`

**Sources:**
- `web.md`: 4a (scene and sequel), 4g (game-master principles), 5 (Marinara rules), 6 item 8.
- `chub.md`: "Narrator – Between the Worlds" (owner's card 353), "Storyteller", TaleScribe structure only (one action and one line per character per turn; only the first proposed action is taken).
- `local.md`: talemate director templates (paraphrase); multi-NPC rules in "Concrete techniques".

**Body must contain:**
- One move per reply, then "what do you do?" in the character's voice; the
  move list adapted from Apocalypse World (reveal an unwelcome truth, show
  a threat coming, offer an opportunity at a cost, put someone in a spot,
  turn their move back on them, take away their stuff, separate them,
  advance a threat off-screen).
- Try-fail: yes-but and no-and outcomes; every `{{user}}` action is an
  attempt.
- Scene and sequel: goal → conflict → disaster; reaction → dilemma →
  decision.
- Multi-character scenes: one character leads each reply; characters act
  only on what they could know.
- `post_history_instructions` as the game-master layer, with `{{original}}`
  placed after the card's text so the user's own prompt keeps priority.
- Reply length varies by scene: short in action, longer in quiet moments.

**References:** `gm-moves.md`: each move with a one-line roleplay example.

**Credits line:** `Moves adapted from the principles of Apocalypse World (Vincent and Meguey Baker).`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/story-engine
git commit -m "feat(forge-ai): add story-engine skill"
```

---

### Task 6: `arc`

**Files:**
- Create: `forge-ai/skills/arc/SKILL.md`
- Create: `forge-ai/skills/arc/references/lie-want-need.md`

**Frontmatter description (exact):**
`Steer a card in a direction the owner names, such as darker, lighter, redemption, corruption, or a genre, by changing what the character believes, wants, and needs rather than adding adjectives. Use when the owner asks to take a card somewhere, shift its tone, or give it an arc.`

**Sources:**
- `web.md`: 4a (Weiland arcs), 6 items 1, 2, 7.
- `local.md`: causal construction, "one unresolved memory".
- `chub.md`: "Techniques worth turning into skills".

**Body must contain:**
- The four parts: the Lie the character believes, the Truth, Want vs Need,
  the Ghost (the wound behind the Lie). How to find each in an existing card.
- A direction is a change to these: darker = the Lie deepens or Want wins;
  redemption = Need is reachable; a genre shift = new stakes and setting
  pressure on the same Lie. A table of common directions with the edit each
  makes.
- Tone changes through `mes_example` rewrites, not adjectives in
  `description`; examples written as history from before the greeting so
  the model doesn't replay them.
- The core identity stays: name, Ghost, speech habits, unless the owner
  asks to change them.
- How each field carries the arc: `personality` (the Lie as behavior),
  `scenario` (pressure on it), `first_mes` (the Lie on display),
  `post_history_instructions` (pace of change), lorebook (moments that test
  it).

**References:** `lie-want-need.md`: a worked example taking one adult
character in three directions, field by field.

**Credits line:** `Arc model after K.M. Weiland, Creating Character Arcs.`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/arc
git commit -m "feat(forge-ai): add arc skill"
```

---

### Task 7: `reveals`

**Files:**
- Create: `forge-ai/skills/reveals/SKILL.md`
- Create: `forge-ai/skills/reveals/references/patterns.md`

**Frontmatter description (exact):**
`Give a card hidden motives and staged reveals through its lorebook: what characters believe versus what is true, hints before facts, and escalation stages keyed to words that come up as the story advances. Use when the owner asks for secrets, a mystery about the character, a slow reveal, or a relationship that develops in stages.`

**Sources:**
- `~/git/sillytavern-skills/skills/character-card-v3-generator/references/lorebook-guidelines.md`
  and `examples/secret-reveal-pattern.md`.
- `local.md`: lorebook steering in "Concrete techniques".
- `web.md`: 3, 6 items 3 to 5.
- `forge-ai/prompts/revise-lorebook.tmpl` and `lore-entries.tmpl` (what the
  editor can set).

**Body must contain:**
- Keyword gating only: a hint entry keyed to everyday words; a revealed
  entry keyed to words that only come up after the event, narrowed with
  `secondary_keys`. State plainly that timing settings aren't available.
- Belief entries vs truth entries, with `comment` naming which is which.
- A physical tell in `description` or `personality` that points at the
  secret without stating it.
- Escalation stages: a status line in `post_history_instructions` (such as
  trust or suspicion stage) and an entry per stage keyed to that stage's
  word.
- When `constant` is right (almost never for a secret).
- Keep secrets out of `description` and `first_mes`; they're sent every
  turn or shown first.

**References:** `patterns.md`: three patterns (the hidden motive, the
belief/truth pair, the relationship ladder), each with the full lorebook
entries as `revise-lorebook` JSON.

**Credits line:** none.

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/reveals
git commit -m "feat(forge-ai): add reveals skill"
```

---

### Task 8: `scene-combat`

**Files:**
- Create: `forge-ai/skills/scene-combat/SKILL.md`
- Create: `forge-ai/skills/scene-combat/references/checklist.md`

**Frontmatter description (exact):**
`Make a card run fights that matter: every fight changes something that stays changed, pace follows sentence length, and injuries and exhaustion persist. Use when the owner asks for better combat, action scenes, or a fighter character.`

**Sources:** `web.md` 4b; `local.md` "Scene writing, pacing, greetings and
steering" (event pipeline, weighted outcomes); talemate director combat
template (paraphrase).

**Body must contain:**
- A fight changes something irreversibly: a wound, a loss, a revealed
  truth, a shifted relationship.
- Choose the emotional effect (dread, triumph, desperation) before the
  moves.
- Short sentences in fast exchanges, longer ones in lulls.
- Injuries, fatigue, and spent resources carry forward; a lorebook entry
  per lasting consequence.
- `{{user}}`'s attacks are attempts the character reacts to; the character
  never decides whether `{{user}}`'s blow lands on `{{user}}`'s behalf
  beyond the immediate result.
- How this character fights when winning and when losing (`personality`).
- One `mes_example` exchange showing a cost.

**References:** `checklist.md`: a pre-fight and post-fight checklist.

**Credits line:** `Combat principles after Fonda Lee's writing on fight scenes.`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/scene-combat
git commit -m "feat(forge-ai): add scene-combat skill"
```

---

### Task 9: `scene-romance`

**Files:**
- Create: `forge-ai/skills/scene-romance/SKILL.md`
- Create: `forge-ai/skills/scene-romance/references/checklist.md`

**Frontmatter description (exact):**
`Make a card play romance that builds: a reason the two aren't together yet, beats that move the relationship, slow-burn near-misses, and an outside plot that keeps going. Use when the owner asks for romance, slow burn, a love interest, or more chemistry.`

**Sources:** `web.md` 4c, 6 item 5; `local.md` example-dialogue
"vulnerability, intimacy, or want" slot.

**Body must contain:**
- The obstacle: why they aren't together, rooted in the character's Lie or
  Want (link to `arc` by name).
- Beats adapted from Gwen Hayes's romance structure, mapped to what a card
  can hold: alternate greetings at different stages, lorebook entries per
  stage.
- Slow burn: near-misses, interruptions, pining shown through behavior.
- The outside plot keeps moving so romance isn't the only thing happening.
- Chemistry through friction and specific attention, not compliments.
- The character pursues and withdraws on their own motives; `{{user}}`'s
  feelings are never stated for them.
- A `mes_example` exchange showing vulnerability.

**References:** `checklist.md`: a beat ladder with what each beat needs in
the card.

**Credits line:** `Beat structure after Gwen Hayes, Romancing the Beat.`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/scene-romance
git commit -m "feat(forge-ai): add scene-romance skill"
```

---

### Task 10: `scene-intimate`

**Files:**
- Create: `forge-ai/skills/scene-intimate/SKILL.md`
- Create: `forge-ai/skills/scene-intimate/references/checklist.md`

**Frontmatter description (exact):**
`Make a card's intimate scenes carry meaning: emotional stakes, what changes between the characters, consent shown as part of the scene, and rhythm that follows intensity. Keeps the card's existing level of explicitness. Use when the owner asks to improve intimate or sexual scenes.`

**Sources:** `web.md` 4d; talemate unslop romance example (paraphrase).

**Body must contain:**
- Start from stakes: what each character risks and what changes after.
- Consent as craft: the presence of a yes, visible signals, checking in in
  character, a pause before an answer as tension.
- Rhythm follows intensity; sensory detail chosen, not catalogued.
- The character's own wants, limits, and aftermath behavior (`personality`).
- `{{user}}`'s body, sensations, and reactions are never written for them.
- Explicitness matches what the card already has; this skill doesn't raise
  or lower it. Adult characters only.
- Written at craft level: the skill itself contains no explicit text.

**References:** `checklist.md`: before, during, and after checklist.

**Credits line:** none.

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/scene-intimate
git commit -m "feat(forge-ai): add scene-intimate skill"
```

---

### Task 11: `scene-horror`

**Files:**
- Create: `forge-ai/skills/scene-horror/SKILL.md`
- Create: `forge-ai/skills/scene-horror/references/checklist.md`

**Frontmatter description (exact):**
`Make a card build dread instead of gore: suspense the reader sees coming, wrongness in small details, and a threat that escalates by rules. Use when the owner asks for horror, dread, tension, or a scarier card.`

**Sources:** `web.md` 4e (Hitchcock suspense vs surprise, the horror
failure-mode checklist from `haowjy/creative-writing-skills`).

**Body must contain:**
- Suspense over surprise: show the threat to the reader before the
  character knows.
- Small wrong details over monsters; withhold the full view.
- The threat follows rules the story reveals; escalation through lorebook
  stages (link to `reveals` by name).
- Pacing: quiet stretches make the spikes land.
- Failure modes: gore as a substitute, explaining the monster, constant
  maximum intensity.

**References:** `checklist.md`: the horror failure-mode checklist in
original wording.

**Credits line:** `Failure-mode layout adapted from haowjy/creative-writing-skills (Apache-2.0).`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/scene-horror
git commit -m "feat(forge-ai): add scene-horror skill"
```

---

### Task 12: `scene-mystery`

**Files:**
- Create: `forge-ai/skills/scene-mystery/SKILL.md`
- Create: `forge-ai/skills/scene-mystery/references/checklist.md`

**Frontmatter description (exact):**
`Make a card run an investigation {{user}} can solve: three clues for every conclusion, a new event when the trail goes cold, and characters who know only what they could know. Use when the owner asks for a mystery, an investigation, a detective, or a puzzle.`

**Sources:** `web.md` 4f (Three Clue Rule), 6 item 9 (`{{random}}` once per
chat); `local.md` book2world "what's true vs what the character knows".

**Body must contain:**
- The Three Clue Rule: every conclusion `{{user}}` must reach has at least
  three clues, any one of which can lead there.
- The solution lives in lorebook entries, not `description`; clues are
  entries keyed to where they're found.
- When `{{user}}` stalls, something happens: a new event, a witness, a
  second crime.
- Characters answer from what they know and lie for their own reasons.
- `{{random}}` in `first_mes` to pick the culprit once per chat, with the
  matching lorebook entries.
- Fair play: the clues are in the story before the reveal.

**References:** `checklist.md`: a clue map template.

**Credits line:** `Three Clue Rule from Justin Alexander (The Alexandrian).`

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the files.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/scene-mystery
git commit -m "feat(forge-ai): add scene-mystery skill"
```

---

### Task 13: `greetings`

**Files:**
- Create: `forge-ai/skills/greetings/SKILL.md`

**Frontmatter description (exact):**
`Write or rework a card's first message and alternate greetings as separate entry points: each opens its own situation, shows how the character talks, and ends by handing {{user}} the next move. Use when the owner asks for new greetings, better openings, or more ways into a card.`

**Sources:** `web.md` 2f, 6 item 6; `chub.md` 3 (Prosa), 4 (WW+);
`forge-ai/prompts/alternate-greetings.tmpl` (don't repeat what it already
says).

**Body must contain:**
- Each greeting is a different situation, time, or relationship stage, not
  a rewording.
- `description` and `mes_example` must fit every greeting; fix them when a
  greeting contradicts them.
- A greeting shows the character's voice in the first lines and ends on a
  hook: a question or action that leaves `{{user}}` the move.
- The greeting's length sets the reply length the model will copy.
- `creator_notes` summarizes each greeting in one line so the player can
  choose.
- `{{user}}` never acts, speaks, or feels in a greeting.

**References:** none.

**Credits line:** none.

- [ ] **Step 1:** Read the sources.
- [ ] **Step 2:** Write the file.
- [ ] **Step 3:** Run the verify commands. Expected: PASS, 150 to 300 lines, `1`.
- [ ] **Step 4: Commit**

```bash
git add forge-ai/skills/greetings
git commit -m "feat(forge-ai): add greetings skill"
```

---

### Task 14: Document the bundled skills

**Files:**
- Modify: `forge-ai/README.md` (`## Skills`, after `### Import`)

- [ ] **Step 1: Add a section**

After the `### Import` subsection's last paragraph, add:

````markdown
### Bundled skills

`skills/` holds ten skills for Reforge. Mention one with `@skills/<name>`,
or let the model load it when its description fits.

| Skill | For |
|---|---|
| `card-craft` | Improving any card |
| `greetings` | First message and alternate greetings |
| `story-engine` | Cards that drive the plot |
| `arc` | Steering a card in a direction you name |
| `reveals` | Secrets and staged reveals in the lorebook |
| `scene-combat`, `scene-romance`, `scene-intimate`, `scene-horror`, `scene-mystery` | Cards that play those scenes well |

A skill with reference files imports only as a zip, since a raw `SKILL.md`
URL leaves the references out. Zip the folder and upload it:

```bash
cd forge-ai/skills && zip -r card-craft.zip card-craft
```
````

- [ ] **Step 2: Check the command**

```bash
cd ~/git/innkeeper-plugins-reforge-skills/forge-ai/skills && zip -r /tmp/card-craft.zip card-craft && unzip -l /tmp/card-craft.zip
```

Expected: lists `card-craft/SKILL.md` and `card-craft/references/*.md`.

- [ ] **Step 3: Commit**

```bash
git add forge-ai/README.md
git commit -m "docs(forge-ai): describe the bundled skills"
```

---

### Task 15: Final check

- [ ] **Step 1: Run what CI runs**

```bash
cd ~/git/innkeeper-plugins-reforge-skills
go vet ./... && go test -race ./... && make check
```

Expected: PASS, except `TestEnrichCancelStopsFurtherCalls`.

- [ ] **Step 2:** Read the full diff (`git diff origin/main`) for copied
  text from AGPL or unlicensed sources, `{{user}}` actions in examples,
  minors in examples, and lorebook advice using delay, sticky, or cooldown.
- [ ] **Step 3:** Report to the owner. Don't push; the owner decides where
  this branch goes.
