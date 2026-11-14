# Reforge skills: design

forge-ai can already store, import, and apply Agent Skills (see
[forge-ai README § Skills](../../forge-ai/README.md#skills)), but no skills
exist. This design adds ten skills that make Reforge improve a card's craft
and steer its story, plus one fix so a whole-card Reforge plans with the
mentioned skills.

## Principle

A skill turns craft into edits to card fields. It never just tells the model
to "write better fights" or "make it darker". For example, `scene-combat`
says what each field needs:

| Field | Edit |
|---|---|
| `mes_example` | One exchange: the user acts, the character reacts, the cost carries forward |
| `post_history_instructions` | Every fight changes something; injuries persist |
| `personality` | How this character fights when losing |
| lorebook | An entry per consequence a fight can leave |

A direction is structural in the same way. A slow-burn edit rewrites the
character's Want and Lie, adds concealed and revealed lorebook entries, and
adds alternate greetings set at different relationship stages.

## Skills

| Skill | Use when the owner asks to | Core material |
|---|---|---|
| `card-craft` | improve any card | Causal construction (trait → cause → observable behavior, shared cause repeated across fields), testable traits, failure behavior, tiered preferences, field mechanics, how example dialogue trains the model, cutting purple prose |
| `greetings` | add or rework greetings | Each alternate greeting is a separate entry point with its own situation, ending on a hook for `{{user}}`; the description and examples fit every greeting; `creator_notes` summarizes each |
| `story-engine` | make the card drive the story | One game-master move per reply, then hand `{{user}}` a choice (Apocalypse World moves); every user action is an attempt that can fail or succeed at a cost; scene and sequel; `post_history_instructions` as the game-master layer |
| `arc` | steer the card in a direction, named in the instruction | The Lie, the Truth, Want vs Need, the Ghost (Weiland); a direction changes these, not adjectives |
| `reveals` | add hidden motives or staged reveals | Concealed and revealed lorebook entries, belief vs truth, escalation stages and the entries keyed to each |
| `scene-combat` | make fights play well | A fight changes something irreversibly; sentence length sets pace; injuries and exhaustion persist (Fonda Lee) |
| `scene-romance` | add or improve romance | Romance beats (Gwen Hayes), slow-burn near-misses, an outside plot that keeps moving |
| `scene-intimate` | improve intimate scenes | Emotional stakes and what changes between the characters, consent as craft, rhythm following intensity |
| `scene-horror` | add dread or horror | Suspense over surprise (Hitchcock), horror failure modes |
| `scene-mystery` | add an investigation | Three Clue Rule (Justin Alexander), a new event when the user stalls, what each character could know |

`arc` covers every tone or genre direction ("@skills/arc toward redemption",
"@skills/arc darker"), so there is no skill per tone.

`reveals` gates reveals with keywords only: a revealed entry's keys are
words that come up after the event, narrowed by `secondary_keys`.
`revise-lorebook` can set only `comment`, `keys`, `secondary_keys`,
`content`, and `constant`; delay, sticky, and cooldown are SillyTavern
extensions core doesn't model. Add them when a card needs timed effects.

`scene-intimate` works at craft level and keeps the card's existing level of
explicitness.

## Skill anatomy

Each skill is a folder with a `SKILL.md` and optional `references/`:

- **Frontmatter `description`:** says when to use the skill, so
  `load_skill` picks it only when it fits. Scene and direction skills start
  "Use when the owner asks for …", so the model doesn't apply romance to
  every card.
- **Body, about 150 to 300 lines:**
  - **Protect:** what the edit keeps: voice, point of view, facts, credits.
  - **Failure modes:** how models get this wrong, with examples.
  - **Edits per field:** a table like the one under [Principle](#principle).
  - **Done when:** checks a reader can verify, such as "would a reader
    know this trait was violated?"
- **References:** longer checklists and before/after examples.

Every skill repeats these rules, because each may be used alone:

- Never write `{{user}}`'s actions, words, or thoughts.
- State rules as what to do; naming a behavior to forbid it steers the
  model toward it.
- Never compress example dialogue; it sets the character's voice.
- Bring `creator_notes` in line with the edit.

## Layout and delivery

```
forge-ai/skills/
  card-craft/      SKILL.md, references/{fields,examples,unslop}.md
  greetings/       SKILL.md
  story-engine/    SKILL.md, references/gm-moves.md
  arc/             SKILL.md, references/lie-want-need.md
  reveals/         SKILL.md, references/patterns.md
  scene-combat/    SKILL.md, references/checklist.md
  scene-romance/   SKILL.md, references/checklist.md
  scene-intimate/  SKILL.md, references/checklist.md
  scene-horror/    SKILL.md, references/checklist.md
  scene-mystery/   SKILL.md, references/checklist.md
```

The owner imports a skill with the existing importer, as a zip of its
folder. A raw `SKILL.md` URL works only for a skill without reference
files, since it leaves the references out. To update one, edit it in git and import
it again. The plugin doesn't embed or seed skills.

## Sources and licenses

| Source | License | Use |
|---|---|---|
| `davidgibbons/sillytavern-skills` `character-card-v3-generator` | own | Adapt freely: causal construction, testable traits, lorebook reveal patterns, multi-NPC rules |
| `haowjy/creative-writing-skills` | Apache-2.0 | Skill layout (protect, failure modes, revision); credit it in each skill that borrows |
| talemate `unslop` and director templates | AGPL | Paraphrase ideas only; copy no text |
| Marinara preset | none | Paraphrase narrative rules only; leave out its jailbreak text |
| Chub creator cards, community guides, craft books | various | Ideas only, in original wording |

Leave out anything from the excluded Chub cards (TaleScribe 142156,
Character Generator 111756 and siblings, Character Bot Creator Helper
246287).

The research notes, with URLs and each source's concrete rules, are the raw
material for writing the skills:
[local repos](2026-11-13-reforge-skills-research/local.md),
[web](2026-11-13-reforge-skills-research/web.md), and
[Chub creator cards](2026-11-13-reforge-skills-research/chub.md).

## Code change: plan with the mentioned skills

A whole-card Reforge's plan call (`wholecard.go` `plan`, `prompts.go`
`planPrompts`) gets neither the mentioned skills nor tools, so it picks
fields without knowing the direction. Change:

- `plan` loads the mentioned skills with `forcedSkills` and appends
  `skillsPrompt(forced)` to the plan system prompt. It still offers no
  tools.
- Test: a whole-card run with a `@skills/` mention sends that skill's text
  in the plan request. It fails without the fix.
- README: say the plan call gets the mentioned skills' instructions.

No core change.

## Trial

Before writing all ten, check that the skill shape helps:

1. Pick six cards from the library with middling `evaluate` quality scores.
2. Reforge each with and without the skill under test.
3. Compare `evaluate` scores and read the pairs side by side.
4. For scene skills, run a short scripted chat and check that the behavior
   shows up.

The comparison is manual; it needs no new tooling.

## Build order

1. Planner fix.
2. `card-craft`, then trial it.
3. `story-engine`, `arc`, `reveals`.
4. The five scene skills.
5. `greetings`.

## Out of scope

- Timed lorebook effects (delay, sticky, cooldown).
- Bundling or auto-seeding skills.
- A slice-of-life skill: no good source; add one if a card needs it.
