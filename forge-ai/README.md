# forge-ai

Writes, revises, tightens, expands, evaluates, and scores character cards,
evaluates lorebooks, and adds alternate greetings, example dialogue, and
lorebook entries, with a language model. It offers the `action`, `records`, `enrich`, and `settings`
capabilities, and calls `ai.chat` on the `ai` instance its `connection`
setting names, so it needs a core with those and an `ai` instance that
offers chat. Scoring can also call `ai.decide` on the instance its
optional `decide` setting names.

## Settings

Settings has two tabs.

- **General** holds `connection`, the `ai` instance every action uses, and
  `temperature` and `max_tokens`. When set, those two replace each
  action's own; when empty, each action uses its own. It also holds
  `decide` and `escalate_below`, for [scores](#scores).
- **Prompts** holds the house style, the whole-card guidance, and each
  action's instructions.

## Actions

The actions fall into four groups by what they do.

| Group | ID | Runs on | Does |
|---|---|---|---|
| Create | `generate` (Forge) | a new card | Writes a card from a concept, in first or third person, or as a scenario card. Takes a reference image |
| Edit | `revise` | a field or the whole card | Changes it as the instruction says. Runs on `name`, the prose fields, and `tags` |
| Edit | `tighten` | a prose field or the whole card | Cuts filler and repetition, keeping every fact |
| Edit | `expand` | a prose field or the whole card | Adds concrete detail without changing what's there |
| Edit | `to-prose` | a prose field or the whole card | Turns lists and bracket formats into paragraphs |
| Edit | `change-pov` | a prose field or the whole card | Rewrites it in first or third person. Skips `creator_notes` |
| Add | `examples` (Write examples) | `mes_example` | Replaces the example dialogue with 1 to 10 new exchanges, 3 by default |
| Add | `alternate-greetings` | a card | Adds 1 to 10 alternate greetings, 3 by default, after the existing ones |
| Add | `lore-entries` | a lorebook | Adds 1 to 20 entries, 5 by default, about what the instruction asks, from the cards it mentions |
| Judge | `evaluate` | a card | Reports a score, six dimension scores, contradictions, misplaced content, and suggestions |
| Judge | `evaluate-lorebook` (Evaluate) | a lorebook | Reports a score, five dimension scores (`triggers`, `consistency`, `focus`, `usefulness`, `token_efficiency`), contradictions, problems in entries, and suggestions. Stores no scores |

An edit on the whole card runs in two steps:

1. One call reads the whole card, the owner's direction, and the action's
   instructions, and writes a note per field, or `skip`. It offers the
   model no tools.
2. The field action runs on each field with a note, in order, with its note
   and the card as the earlier fields left it.

The core keeps only the changes to the action's fields. A plan that skips
every field proposes the card unchanged.

`lore-entries` sends the lorebook's name, description, and each entry's
`comment`, `keys`, and `content`. Existing entries stay as they were. A new
entry has no `id`, `enabled` true, `insertion_order` 100, `position`
`before_char`, and no regex. Keys compare ignoring case. A new entry is
dropped when one of its keys is an earlier entry's first key or more than
half its keys are taken; otherwise it loses only its taken keys. A run that
adds nothing proposes the lorebook unchanged.

`evaluate-lorebook` sends the same, plus each entry's `secondary_keys` and
`constant`. Its findings sit under Entries and name the entries they're
about.

## Scores

The `enrich` capability runs the `evaluate` prompt on each card the owner
picks, one model call per card, and stores its scores, all 0 to 100:

| Key | Score |
|---|---|
| `quality` | The mean of the dimensions, shown on the library grid and as the report's score |
| `consistency`, `voice`, `first_impression`, `roleplayability`, `token_efficiency`, `field_governance` | Each evaluate dimension |

A score outside 0 to 100 is clamped to the nearest end, and a dimension
the model gave no score is left out. A card whose model call fails, or
whose reply has no scores, stores nothing and shows the error; the other
cards still get scored. Scoring offers the model no tools: no skills and no
library.

The core sends cards in batches of up to 20. `concurrency`, 4 by default,
is how many of a batch's cards are scored at once; results keep the batch's
order. A hosted model's rate limit fails the cards it refuses, so lower
`concurrency` if you see rate-limit errors.

Each value records the model the `ai` instance answered with and the
prompt's version: `evaluate-` plus a hash of `prompts/evaluate.tmpl`, the
house style, the Evaluate override, and the temperature and max tokens sent.
Scores whose versions differ came from different setups.

Evaluate on a saved card with no unsaved edits stores the same scores, so
the grid shows the report's numbers and a later scoring run skips that
revision.

### Scoring with a decision model

With `decide` set to an `ai` instance offering `ai.decide`, such as a
vllm-sr/d3 connection, each card gets one `ai.decide` call first instead of
the chat model. It asks four score questions, each with five levels that
describe situations: `first_impression`, `voice`, `roleplayability`, and
`craft`. Each level is scaled to 0 to 100. `quality` is the mean of all
four; `craft` itself isn't stored. The questions see only the card's name,
description, personality, scenario, first message, and examples. Their
values' prompt version is `decide-` plus a hash of the questions.

The chat model scores the card instead, with every dimension, when:

- the run is thorough, as Rescore on a card's page is, so the card's score
  matches its Forge report;
- the decision call fails, such as a card too long for the server; or
- the mean confidence of the four answers is below `escalate_below`,
  0.25 by default.

So `consistency`, `token_efficiency`, and `field_governance` come only from
the chat model.

The two don't score on the same scale. On 40 random Chub cards (October
2026, d3 Q8_0 against Artemis 31B), they ranked cards alike, with a
Spearman correlation of 0.89 on the mean of the four scores, from 0.68 for
craft to 0.87 for voice. But d3 scored higher on average: first impression
65 against 50, craft 61 against 49. Its confidence was low throughout, with
medians from 0.22 to 0.40, and only partly marked the cards where the two
disagreed. Score a sample both ways before comparing values across
versions.

## Prompts

The prompts are in `prompts/`, as `text/template` files with `[[ ]]`
delimiters so `{{char}}` and `{{user}}` reach the model as written. The
generate, revise, and evaluate prompts come from character-card-generator
at commit `0b2f57e`, changed to ask for JSON keyed by CCv3 data keys.

Each prompt's rules are an instructions block, `[[define "<key>.instructions"]]`.
The Prompts tab shows each block as its default.

- An override replaces only the instructions. The frame around them, which
  holds the JSON format and the card, stays fixed, so an override cannot
  change the reply's shape.
- An empty override means no instructions.
- A block left unchanged follows the plugin's default as it improves. Reset
  brings an overridden block back to the default.
- The house style is added to every action's system prompt.
- The whole-card guidance tells the planning call how to write each
  field's note.

## Library access

`generate`, `revise`, `alternate-greetings`, and `lore-entries` may read the
library through `library.call` when the owner turns on Library access for
the instance. A whole-card run's plan call offers no tools; each field's
call does. If the `ai` instance rejects the request with tools, the run
retries once without them, and only the cards and lorebooks the owner
attached reach the model.

## Skills

A skill is an [Agent Skill](https://agentskills.io/specification): a
`SKILL.md` with `name` and `description` frontmatter and Markdown
instructions, plus optional reference files. The plugin keeps skills in its
own database schema, so it needs a core with the `records` capability. The
owner adds, edits, and imports them on the instance's settings page.

Each skill has an Enabled switch. New and imported skills start enabled,
so an import is usable at once; reimporting a disabled skill leaves it
disabled. Only enabled skills reach the model:

- Every action offers the tools `load_skill` and `load_skill_file`.
  `load_skill`'s description lists each enabled skill's name and
  description; it returns the instructions and the reference file paths.
- A `@skills/<name>` mention puts that skill's instructions and its
  reference file paths in the system prompt. A mention of a skill that
  isn't enabled fails the run, and so do mentions whose instructions add up
  to more than 256 KiB.
- If the model rejects tools, the retry without them keeps only the
  mentioned skills.

Once any skill is enabled, every action sends tools, including ones that
otherwise send none, such as `tighten`. A model without tool support then
rejects each run's first request, and every run makes that failed request
before retrying without tools. Disable all skills for such a model.

### Import

Import takes a `SKILL.md` or a zip of a skill folder, uploaded or by an
`http` or `https` URL. A zip is recognized by its content, not its name.
Importing a skill replaces the one with the same name.

| Limit | Value |
|---|---|
| Upload or download | 8 MiB |
| Download timeout | 30 seconds |
| Zip entries | 200 |
| `SKILL.md`, instructions, or one reference file | 1 MiB |
| Instructions and reference files together | 4 MiB |
| Reference files | 64 |

- A zip holds exactly one `SKILL.md`. Its folder is the skill; files outside
  that folder are ignored.
- A zip entry whose path is absolute, contains `\`, or leaves the folder
  with `..` fails the import.
- Files that aren't UTF-8 text, such as images, are skipped and don't
  count toward the size limits.
- URLs may point at private and loopback addresses, such as a Git server on
  your LAN. The plugin runs on your network and importing needs write
  access in Innkeeper, so refusing them would block real uses. A writer can
  make the plugin request any address it can reach, and sees the status of
  a failed request; give write access only to people you trust with that.

## Upgrading from 0.4

0.5 moves the model settings to the `ai` plugin.

1. Before upgrading, copy any `house_style` and `additions` text you want
   to keep. 0.5 drops `provider`, `base_url`, `model`, `api_key`, and
   `additions`, and reads the house style from the Prompts tab.
2. Create an `ai` instance with the old provider, base URL, model, and key,
   with chat ticked, and press Test connection.
3. In the forge-ai instance's General tab, pick it as the AI connection.
   Until you do, every action fails with "pick an AI connection".
4. Paste the house style, and any addition worth keeping, into the Prompts
   tab. An addition was extra text after the default; an override replaces
   the default, so start from the text shown and add to it.
