# Local character-card writing guidance: findings

Read-only survey of ~/git and ~/.claude/skills, 2026-11-13.

## The skill the user remembered

**~/git/sillytavern-skills/skills/character-card-v3-generator/** is the one (origin
github.com/davidgibbons/sillytavern-skills). It is a Codex-style Agent Skill with
`SKILL.md`, `references/`, `examples/`, `assets/templates/`, and `scripts/validate_card.py`.
The last commit is d253889 (2026-09-13), "add causal construction guidance".

### How it reached forge-ai

1. sillytavern-skills (Feb 2026) -> character-card-generator commit **0b2f57e**
   (2026-04-02, "rewrite generation, revise, and evaluate prompts per CCv3 skill").
   That commit added the pre-writing step (archetype, central tension, distinctive
   trait, relationship), the first_mes quality criteria, the 3-context mes_example,
   and the field-governance evaluation.
2. forge-ai's prompts were **ported from character-card-generator at 0b2f57e**
   (innkeeper-plugins/docs/plans/2026-10-15-forge-ai.md:103-104; forge-ai/README.md:137).
3. Not ported:
   - character-card-generator **f01b365** (2026-04-08) adds "state traits as observable
     behaviors ('deflects personal questions with sarcasm before answering honestly' not
     'guarded'). Each trait should be testable: would a reader know it was violated?"
     forge-ai still says only "Define behavior, not adjectives."
   - The skill's **Causal Construction** section (Sept 2026) postdates the port. Nothing
     about causes appears in forge-ai/prompts (grep for causal/testable finds nothing).

There are stale copies of the skill's references at ~/git/worldbuilder/design/character_card_v3/.
They predate the causal-construction section and lack multi-character, popular-patterns and
source-notes. Ignore them.

### Concrete techniques in the skill, by file

**SKILL.md**: a 7-step workflow:
1. Clarify the brief: genre, tone, hard boundaries, relationship to {{user}}, chat style
   (short or long), and template tier (minimal, standard or advanced).
2. Build the skeleton. For upgrades, make a minimal-diff transform.
3. Write high-signal content.
4. Plan the lorebook.
5. Add card-scoped regex.
6. Validate.
7. Return the JSON, with a design-choices note kept outside it.

Rule: keep "spec constraints" separate from "model-behavior heuristics".

**references/character-writing-guidelines.md** (the core one):
- Principles: identity as behavior, not adjectives. Separate hard constraints from style
  flavor. Specific examples over abstractions. One job per field. Wiki or trope material is
  draft input, pruned to what affects behavior.
- **Causal construction**: chain every trait as `trait -> why -> observable behavior`
  (example: speech impediment -> fell off a cliff as a child -> repeats himself when
  startled). Cross-link traits that share a cause, and reference that cause from several
  fields. Anchor **one specific unresolved memory** (a sensory detail or a line of
  dialogue) instead of a backstory narrative. **Leave real tension unresolved**: settled
  arcs remove the friction that makes turns vary. Repeat the load-bearing cause across
  personality, scenario and mes_example so it survives context pressure.
- Field guidance:
  - personality includes conflict-handling style and **fail-state behavior**.
  - mes_example covers different emotional states *and message lengths*.
  - first_mes anchors future response length.
- ST runtime notes: description, personality and scenario are permanent token load.
  first_mes is sent once. Examples get pushed out first. So never store rules only in
  examples.
- Quality checklist:
  - The character summarizes in 3-5 stable traits.
  - The traits are testable in dialogue.
  - No contradictions.
  - Appearance and personality are concrete enough to act on.
  - Every trait traces to a cause.
  - At least one tension stays unresolved.
- Anti-patterns: overlong prose with no behavioral signal, duplicate constraints, purely
  aesthetic traits, trait lists with no causal chain, and a fully resolved arc.
- Community additions: explicit sections for backstory, likes/dislikes, goals/fears,
  quirks, skills, worldview and speech mannerisms; **tiered preferences** (strongly likes
  vs mildly likes) to avoid binary behavior; no single giant paragraph.
- Optional hidden state: `<!-- HIDDEN: ... -->` comments for foreshadowing, unperceived
  environment details and NPC off-screen motives, one short note per turn, with
  leak-risk caveats.

**references/spec-v3-field-map.md**:
- system_prompt is the durable contract: role boundaries, narrative stance, world-logic
  invariants. It must not carry temporary scene state.
- post_history_instructions is high-authority final-turn steering: format, turn-level
  pacing and length, adherence checks, and guardrails for known failure modes
  (repetition, drift, perspective errors).
- Failure pattern: post_history_instructions silently fighting system_prompt. Note:
  `{{original}}` keeps the global prompt inside a per-character override.

**references/reviewing-cards.md**:
- Eight-category 0-2 rubric: spec, consistency, dialogue, lorebook, token efficiency,
  safety/leak, portability, and multi-character. 12-14 means publish-ready; 9-11 means
  usable; 0-8 means revise.
- Test protocol: 4 scripted chats (neutral, conflict, edge case, and **deliberate
  subversion**, where the user pushes against the character's goals). Log failures by
  category, patch the highest-impact ones first, then re-test the same prompts.
- Pass bars: behavior is stable for 20+ turns, holds whether the user plays along or
  pushes back, and shows low repetition across 3 scenarios.
- The card must not control {{user}}'s actions unless designed to.

**references/multi-character-and-scenarios.md**:
- Always-on fields hold world rules, 1-3 anchor factions and hard constraints. The
  lorebook holds rosters, locations, event branches, rare NPCs and time/phase behavior.
- NPCs: a compact cast index (name, role, one-line intent). Split each NPC into one entry
  per behavior domain (motivation, relationships, red lines).
- **Event pipeline**: Detect -> Resolve (one outcome, deterministic or weighted) -> Apply
  (state delta) -> Render (format kept separate). Use event families (politics,
  travel/logistics, **combat/escalation**, social reputation).
- Arbitration: "only one NPC leads each turn unless the scene requires more". Label
  speakers explicitly. Give each NPC one speech cue. No omniscient leakage.

**references/lorebook-guidelines.md**:
- Entry design: one concept per entry, 20-100 words, imperative content.
- Activation: tune scan_depth, budget and insertion_order before adding volume; use
  selective plus secondary_keys.
- **Command orchestration**: `!explore`, `!talkto` and `!tryto` entries ("this turn,
  resolve X, then narrate Y"), with world-logic failure modes and pacing ("brief
  uncertainty before final result").
- **Weighted outcome macros**: 2-4 branches labelled pass, fail, twist or partial, plus
  composable "entry packs" (action_success_state, action_failure_state, ...).
- **Delayed secrets**: an early *directive* hint ("Be evasive about what happened in the
  barn.") and a late *fact* entry gated by delay or trigger. Keep **belief entries** and
  **fact entries** separate, and plan the post-reveal cleanup. Use sticky and cooldown so
  rare reactions stay rare. Keep trigger words out of normal prose in random-roll groups.

**examples/secret-reveal-pattern.md**: public / directive-hint / delayed-fact / belief /
fact entries, run as early (suspicious but vague), mid (conflicting theories) and reveal
(update the beliefs) phases. Includes a QA list.

**examples/command-orchestration-pattern.md**: a template for weighted command outcomes
with ephemeral cleanup.

**references/popular-card-patterns.md**:
- Keep: a first_mes hook that "creates a clear response decision on turn 1", sectioned
  scannable structure, creator notes on play style, dense tags.
- Normalization pass: preserve behavior while reorganizing structure, delete empty or
  broad keys, split giant lore entries, and never dilute the first-message hook.

**references/format-options.md**: Prose / Attributes / AliChat / JED/JED+ / Plaintext / W++,
compared on best use and risks. Selection strategy: start with Attributes or JED, add Prose
for nuance, add AliChat examples when voice drifts, and add regex only for leftover format
drift.

The other references (regex-scoped-scripts, image-and-asset-formats, source-notes) cover ST
plumbing and links (Reddit threads, ST docs, the sphiratrioth666 "lorebooks as ACTIVE
scenario guidance" page). Their value to a card editor is low.

## Other local sources

### ~/git/character-card-generator/src/services/prompts.js
The direct ancestor of forge-ai's generate, revise and evaluate prompts. Its one extra is
the **testable-trait** line (f01b365) quoted above, which is also in the 3rd-person
variant. Its `revise` anti-patterns ("don't improve grammar in untouched sections", "don't
switch prose<->JED unless asked") are already reflected in forge-ai's revise and field
rules. The evaluate prompt has a `formatAppropriateness` dimension that forge-ai dropped.

### ~/git/worldbuilder/prompts/versions/v5/card_write_system.txt
A book-to-card prompt. Distinctive ideas:
- A **JED+ personality template**: `Core drives | Speech | Conflict | Limits | Failure
  mode`, with a worked Dorothy example. This is a good reference-file example.
- "Ground all content in the supplied analysis; do not invent traits".
- Lorebook entries of 10-20 items, 2-4 keys and 20-100 words each.

### ~/git/book2world/backend/prompts/*.md
Book-to-card pipeline roles: card_composer, field_refiner, scenario_ideator, reviewer, and
others. Ideas worth borrowing:
- A **spoiler / known-at-cutoff boundary** (objective truth vs what the character knows).
- The field_refiner's locked fields.
- The scenario_ideator writes 3+ start points, including at least one narrator/world
  start and one character start, phrased as "Do you want to meet Dorothy when the storm
  begins?"

This is useful for a "new greeting from a story moment" skill. Otherwise it is thin.

### ~/git/talemate (davidgibbons fork of veguAI/talemate; no own commits)
This is third-party, but it is the only local source with real **scene-prose craft**:
- `src/talemate/prompts/templates/editor/unslop.jinja2`: few-shot REVIEW/ANALYSIS/FIX
  triples, scored on purple prose, unnatural dialogue (no contractions, "educational
  institution"), over-description (finger-by-finger, exact measurements), length, tense,
  **name overuse**, **talking vs showing** (announcing feelings or physical state instead
  of acting), and characters who narrate their own actions aloud when alone. A decensor
  variant keeps profanity intact. It includes a romance example: cosmic metaphors are
  rewritten as a thumb tracing a palm and plain dialogue.
- Director templates: `generate-scene-types.jinja2` (scene types such as Combat, Long
  Travel and Roleplay, each carrying style and pacing instructions),
  `scene-direction-turn-balance.jinja2` (narrator overused or neglected, neglected
  characters, and a **user-agency reminder** after N autonomous turns), and
  `guide-narration-writing-style.jinja2` (tone examples: humorous, serious, provocative).
- License: AGPL-3.0 upstream. Paraphrase the ideas; don't copy the text.

### ~/git/rp-harness-test/asserts/checks.js
Deterministic RP failure checks:
- length between 30 and 4000 characters
- no impersonation (a `{{user}}:` line)
- no refusal phrases
- no 4-gram repeated 4 or more times

These are the failure modes post_history_instructions should target. They are not writing
guidance.

### ~/git/porter (crucible-trainer) and ~/git/crucible
- porter's `crucible_trainer/prompts/evaluate.tmpl` is an older copy of forge-ai's
  evaluate, with token_efficiency and field_governance in place of premise.
- crucible is a classifier. It has no writing guidance.
- The "suggestion bank" (clustered teacher suggestions) is not populated in the local
  `data/crucible.db`.

### Checked and not relevant
- **autoprose**: its skills (prose-extract, prose-evaluate, prose-interview) model a
  person's stylometrics, not characters.
- **~/.claude/skills/writing-beats, writing-fragments, writing-shape**: article and essay
  composition (grounding, beats). Not fiction.
- **~/.claude/skills/lumiverse**: API mechanics for depth prompts and greetings. It has no
  craft guidance.
- **charexplorer** and **charlibrary**: superpowers docs only.
- **skillhub/storytelling-advisor**: business storytelling.

## Scene writing, pacing, greetings and steering: what exists anywhere locally

- Greetings: first_mes criteria (voice in 2 sentences, concrete scene with stakes, show
  through action and sensory detail, one clear opening, never act for {{user}}, and
  first_mes anchors length and pacing). These are in forge-ai already. Book-moment
  greeting starts come from book2world.
- Steering a card's direction: the skill's delayed-secret, belief-vs-fact, event pipeline,
  command orchestration and weighted outcomes material. Talemate covers turn balance and
  user agency. The skill's post_history_instructions guidance covers failure modes.
- Pacing: only scattered lines ("brief uncertainty before final result", "length/pacing
  reminder" in post_history_instructions, talemate scene types).
- Combat: only the "combat/escalation event family" and pass/fail/twist/partial outcome
  branches. There is no craft guidance (choreography, consequences, injury persistence).
- Romance and intimate scenes: **none** beyond talemate's single unslop example and the
  "vulnerability, intimacy, or want" slot in mes_example. Content must be written fresh or
  sourced externally.

## forge-ai: has vs missing

Has (in prompts/*.tmpl):
- field ownership
- the pre-writing four (archetype, central tension, distinctive trait, relationship)
- first_mes criteria
- 3-context mes_example with independent `<START>` blocks
- the system_prompt vs post_history_instructions split
- scenario-card field placement and cast index
- alternate-greeting rules: a different place, time, mood or stake, matched POV
- lorebook entry and evaluation rules
- the evaluate dimensions consistency, voice, first_impression, roleplayability, premise
- tighten, expand, to-prose and change-POV actions
- the whole-card plan

Missing (candidate SKILL.md content):
1. **Causal construction** (trait -> cause -> behavior, cross-linked causes, one unresolved
   memory, unresolved tension, repeating anchors across fields). Sources: the skill's
   character-writing-guidelines.md, and character-card-generator f01b365 for testable
   traits.
2. **Failure-mode personality** (JED+ `Failure mode:`, fail-state behavior) and
   **tiered preferences**. Sources: worldbuilder v5 and the skill.
3. **Review / test protocol**: the 0-2 rubric, the 4 scripted chats including
   subversion, and the 20+ turn stability bar. Source: reviewing-cards.md.
4. **Story steering via lorebook**: secret reveal, belief vs fact, delay/sticky/cooldown,
   event pipeline, command orchestration, weighted outcomes. Source: lorebook-guidelines.md
   and the two examples. forge-ai's lore-entries covers only factual topic entries.
5. **Multi-NPC arbitration** (one lead speaker per turn, speaker labels, no omniscient
   leakage). Source: multi-character-and-scenarios.md. forge-ai's scenario prompt has a
   fragment.
6. **Format choice** (Prose / Attributes / AliChat / JED+, and when to convert). Source:
   format-options.md and the format-comparison example.
7. **Anti-slop prose editing** (purple prose, name overuse, telling instead of showing,
   robotic dialogue). Source: talemate unslop (paraphrase it; AGPL).
8. **Scene craft: combat, romance, intimate scenes, pacing.** Nothing local. Needs
   external research or original writing.
9. Hidden-state XML comments. Optional, with leak caveats.

The skill folder's layout (SKILL.md plus references/ and examples/) already matches the
Agent Skills format forge-ai imports (README.md:192-235). It could be zipped and imported
nearly as-is, but its SKILL.md workflow assumes it generates a whole CCv3 JSON card and
runs validate_card.py. For Reforge it would need a SKILL.md rewritten around editing, or
splitting into smaller skills (card-craft, lorebook-steering, card-review).
