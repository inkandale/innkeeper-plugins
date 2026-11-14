# Card-creator and narrator cards: what techniques they encode

Research date: 2026-11-13. Read-only; no library data changed.

## Sources and limits

- **Live Chub is blocked from here.** `api.chub.ai/search` returns "This service is
  not available in your country." I used the **Chub archive library (Innkeeper
  library 2, 269k cards, snapshot up to about Jan 2025)** instead, through the
  innkeeper MCP tools. Links below are `chub.ai/characters/<full_path>` from each
  card's `extensions.chub.full_path`. Innkeeper IDs are library-2 card IDs.
- The archive has no star or download counts. The `rating` enrichment (instance 5)
  is 0–5 and nearly everything is a 5, so sorting by it is useless. I found
  candidates through tag filters (`Character Creator`, `botcreator`, `Helpers`,
  `utility`, `Narrator`) and name search, then read about 25 definitions in full.
- Library 1 (the user's own) has no creator cards. It does have narrator cards;
  the user's own `Narrator` (id 353, Fatal_Error_418, "Between the Worlds") is
  the best narrator card I read.
- Libraries 3–7 (JanitorAI, Venus, Pygmalion, RisuRealm) weren't searched in
  depth. A quick RisuRealm search found one `Character Creator Bot`
  (id 481137, not read).

## Verdict

Almost every creator card has the same shape: "You are a card-creation tool",
then one template (W++, PList, or a markdown sheet), then "write a first
message". Most are 100–500 words and teach nothing beyond the template. About
five have methods worth stealing. Several are tuned for one fetish, and two
build sexual content around minors (see Excluded). The narrator cards are
thinner still, apart from the user's own and statuotw's Storyteller.

## Cards with real substance

### 1. Delphi Character Creation Module (QuadraMagic), id 65329
`chub.ai/characters/QuadraMagic/delphi-character-creation-module-420bcd182106`

The best-engineered template. Techniques:
- **Inline per-field instructions.** Every template field carries a `#` comment
  telling the model how to fill it: "Height/Weight: #X'Y\" / Z cm / A lbs / B kg",
  "Nickname(s): #include mundane shorthand names e.g. Joe for Joseph".
- **Omit, don't fill with filler.** "Always omit fields with irrelevant or
  not-applicable. 'Omit' means do not include in sheet at all." (Compare Aiemm's
  "never write Unknown or None".)
- **Minimum counts on lists:** Positives, Negatives, Hobbies, Likes and Dislikes
  each "At least 7". This forces flaws to be as numerous as virtues.
- **Section order with a "Brief Outline" first in each section**, then the details.
- **Generate only on request.** "NEVER prints a sheet of its own volition", so
  conversation and generation stay separate.
- Creator notes: the same template stretches to locales, items, religions and
  fleets. It can convert sheets from other formats and upgrade old ones. When
  output gets cut off, ask for the sheet section by section.
- Weak spots: very long, anthro- and NSFW-anatomy heavy, backstory capped at
  "five paragraphs max".

### 2. Interactive Character Creator (velvety_plastic_8799), id 97826
`chub.ai/characters/velvety_plastic_8799/interactive-character-creator-c59e7d8cea1b`

The best interaction loop in the set.
- **Running summary every turn.** Each reply ends with "(Summary: …)" holding the
  current state of the character, "even if no changes were made". It is the
  working draft, kept in view.
- **Voice-testing while designing.** Each turn has three in-character sample
  lines with action beats, so the user hears the character before committing.
- **Divergent options up front.** Given "Werewolf!", it offers three distinct
  concepts (soldier-turned-PI, a teen torn between worlds, a lonely elder), each
  with a voiced sample, then asks which to develop.
- "Yes-and" posture: never reject the user's idea; improvise around it.
- It lists traits to cover: goal, weaknesses, powers, turn-offs and so on.
- Advice in the greeting: lower frequency and presence penalties so the summary
  isn't paraphrased away.

### 3. Prosa – Character Creation Assistant (kjofoasis), id 80408
`chub.ai/characters/kjofoasis/prosa-character-creation-assistant`

From 2023, and the ancestor of several others (Neuro, id 160765, is a fork).
- **First branch: original or established character?** For an original
  character, ask a few questions (theme, physicality, personality). For an
  established one, answer straight away.
- **In-universe rule:** "Do not mention the franchise or any real world
  information inside the character description … Write it like it is an
  in-universe biography."
- **Register split:** the assistant talks casually, but the card prose is
  "formal, informative, and factual".
- **Greeting rule:** the first message "should introduce the character and how
  they would talk … closely related to their mind and personality". It gives
  one worked example with a hook (the character barges in mid-conflict, holds
  the user at weapon-point, and asks a direct question).
- Card shape: a one-paragraph summary, then labelled lines (Appearance,
  Clothing, Equipment, Abilities, Mind, Personality).

### 4. WW+ Character Creator (Aiemm), id 28068
`chub.ai/characters/Aiemm/ww-character-creator-21d30f48`

The only card that gives numeric guidance on field sizes and lengths.
- Descriptors per field: 5–10 for Mind, Personality and Body; 5–10 for Clothes
  and Likes/Dislikes; 3 for Fetishes. Total card 150–500 tokens.
- **"Ensure dislikes are clearly stated for positive AI bias"**: list dislikes
  explicitly to counter the model's drift toward agreeableness.
- "Emphasize traits by using synonyms" (repetition as weighting); Attributes is
  for non-body details only.
- **Greeting rules:** 150–300 tokens, a scene where the character acts toward
  {{user}}, and it must **end with a question or an action** that hands the turn
  over. Example dialogue: 50–100 tokens per exchange, with a worked `<START>`
  example.
- Sibling `Character Card Creator` (Aiemm, id 3849) does the same in PList with
  Ali:Chat examples. Its useful ideas: "maintain high detail without using
  Unknown/None", "avoid writing facts about the world/system rules" in the
  character card, and **example dialogue framed as an interview** ("tell me
  your life story") so the character's voice shows through self-description.

### 5. Adeline the Character Creator (sneakyfocks), id 94468
`chub.ai/characters/sneakyfocks/adeline-the-character-creator-db86fbf681c4`

Fetish-tilted, but it has the clearest explanation of what each card field is
for, and an editing rule.
- **Staged fields:** description first, then offer personality summary, scenario
  and examples one at a time, waiting for the user between stages.
- Field purposes: **Scenario** sets up how {{user}} meets the character, with
  no dialogue. It is "a great place to put clothing" because it sits low in
  context and persists. **Personality summary** reinforces what matters most.
  **Examples** teach style, with `<START>` and speaker labels.
- **Expand vague words into concrete keywords** (one general adjective becomes
  several specific body or trait descriptors).
- Don't mix a trait with its antonym in the same list.
- **Edit discipline:** "If you are tasked with editing a card, only edit out the
  pieces of info you're asked to edit, and keep its original formatting and
  style." This maps directly to an editor skill.
- A depth-4 injected reminder keeps it on task: "must stick to the guidelines …
  never omit any details".

## Narrator, scene and GM cards

### Narrator – "Between the Worlds" (Fatal_Error_418), user library id 353
The strongest narrator card found, and the user's own. It is long, but the
sections that carry the method are:
- **Role contract:** "Your sole purpose is to be the world in which {{user}}
  acts." Never speak for {{user}} or narrate their thoughts. Don't address
  {{user}} as the Narrator. **"Your turn ends when the narrative calls for
  {{user}}'s action or response."**
- **A "Storytelling Philosophy" section** naming the theme ("The Core is
  Psyche"), with directives: slow, realistic arcs; trust built through small
  actions; trauma and coping, so not every NPC is grateful; NPC-to-NPC dynamics;
  **"Subtlety over Spectacle"**, where quiet moments carry more weight than
  magic.
- **Pacing by design.** Knowledge arrives slowly through random books and NPCs.
  "The environment itself always remains passive and indifferent; it offers no
  helping hand or intentionally placed clues." This stops the model from making
  things convenient.
- **Hard world rules stated as laws** (indestructible house, artifacts driven by
  visualization rather than wishing, a bookshelf that only offers works with two
  or more copies), each with an explicit "no influence" or "no autonomy" clause.
- Two alternate greetings mark different points in the story: the start and
  a late-game epilogue.
- Creator notes give lorebook settings: scan depth 6–10, budget 2048–4096.

### Storyteller – Space Adolion (statuotw), id 141277
`chub.ai/characters/statuotw/space-adolion-storyteller-dcdcfe686e5b`
- A GM card meant to be added to group chats to voice NPCs and story beats.
- **A "primary tools" toolkit:** World Info, Antagonists, Factions and Plotlines,
  each a short paragraph the GM draws events from.
- Pressure rules: "Characters should not be easily beaten", "force situations
  where the players have to choose between two bad outcomes", be "ruthless".
- Example dialogue is three standalone NPC scenes, each ending on an unresolved
  hook (a pirate's ultimatum, a staffer who mouths "Go", a mechanic warning that
  the mech isn't ready).
- Known failure, admitted in the notes: it speaks for the user, and it works
  better dropped into an ongoing chat than started cold.

### TaleScribe (Y0R1K0), id 142156
The content is objectionable: it encourages loli/shota and other extremes, so
don't reuse its text. Its **structure** is the most mechanically inventive
of the GM cards:
- **Turn format:** a "still frame" of the scene with characters' positions, then
  exactly one action and one line per character present.
- **User-input discipline:** only the first of several proposed actions is
  taken, and actions that don't fit the user's persona may fail. The likely
  outcome wins over the convenient one.
- **Plus-commands as a control surface:** `+restart`, `+reroll`, `+comment` (the
  GM explains its plot reasoning), `+charsum` (summarizes how {{user}} has
  developed), `+auto` (the GM plays the user's turn), `+deusex`, and a
  `+lesslewd`/`+morelewd` 0–10 dial.
- **Opening-scene checklist:** something typical for the user, something that
  disrupts normal life, something suspicious, something crazy, something
  promising, something dangerous, and a premise.
- **Cast tiers:** primary characters (antagonist, love interest, mentor) are
  plot-load-bearing and rarely die; secondary ones are vivid but disposable.
  Never describe a "crowd"; be specific about each person.

### Thin narrators (for completeness)
- `Narrator` (daishi, id 170263): a detached third-person observer for group
  chats; mute it and trigger it when needed. One paragraph.
- `The Narrator` (Yaethi, id 121091): a bullet list. The useful note is in the
  changelog: they rephrased the rules to avoid the "'no' curse some models
  have", turning negative instructions into positive ones.

## Utility cards (narrow but relevant to an editor)

- **Character Summary** (panasane, id 96995): a muted group-chat bot that
  turns an NPC who appeared in play into a lorebook entry, using a sheet that
  includes "RELATIONSHIP WITH {{user}}" and "SPEECH: monotone? animated? long
  pauses?". The workflow it suggests: chat-introduced NPCs become lorebook
  entries.
- **Persona Critic** (seliselye, id 166728): critiques the user's persona
  text. Rules: always constructive; tone set by keyword ("roast me" or "please
  be gentle"); even a roast must "draw facts from the bio without making up
  descriptions". Shallow, but it's the only "card doctor" in the archive.
- **Token Reducer** (Zolilio, id 158259): compresses text into a dense,
  non-human-readable form. The creator warns to strip macros first and
  **never compress example dialogue**, because that changes how the character
  talks.
- **The Biographer** (utilbot, id 50337): one describer in three modes
  (elaborate, simple, and pseudo-code "functional"), so the same subject can be
  produced at different verbosity.
- **W++ Converter** (id 135204) and **character_writer** (id 174223, JSON):
  single-format converters, nothing else.

## Shallow (read, little to extract)

Character Card Creator (Aiemm, 3849; PList + interview examples, noted above),
Character Creator V3 (41648; fixed section order description → extra details →
personality summary → traits → scenario → first message, but tuned to
size fetish, "at least 1000 words"), Nyanko the Bot Maker (99069; JanitorAI
fields, rules "never speak for {{user}}", "use uncommon names", "in-chat name
differs from name", a good worked example), Sakura – Character Creation 2
(27345; offers a format choice of W++/Boostyle/plain), Neuro (160765;
Prosa fork for a fetish genre).

## Excluded

- `Character Generator` (Gripping_Minor_0001, ids 111756/98475/65412): built
  to generate sexual content involving minors. Not used.
- `Character Bot Creator Helper` (catbox, id 246287): its worked example is an
  8-year-old in a tsundere frame. Its format (Name/Age/Appearance/Personality/
  Background) is trivial anyway.
- TaleScribe: content excluded, structure kept (see above).
- `The Mare Maker's Manual` (119049) and `The Game Master` (128374) came up in
  the searches but are ordinary roleplay cards, not tools.

## Techniques worth turning into skills

1. **Intake branch:** original or established character? For an original, ask
   3–5 questions (theme, role toward {{user}}, look, personality, the conflict
   they bring). For an established one, draft straight away (Prosa).
2. **Offer three distinct concepts** with one voiced line each before committing
   (Interactive Character Creator).
3. **Keep a running draft summary** at the end of each turn (Interactive
   Character Creator).
4. **Template with inline per-field guidance; omit fields that don't apply**,
   never write "Unknown" (Delphi, Aiemm).
5. **Balance and contrast:** a minimum count of flaws alongside virtues;
   explicit dislikes against the model's agreeableness; no trait listed next to
   its antonym (Delphi, WW+, Adeline).
6. **Size budgets:** card 150–500 tokens of traits, greeting 150–300, each
   example exchange 50–100 (WW+).
7. **Greeting rules:** in-universe, no franchise or meta talk. Show voice and
   personality through action. Open mid-situation. **End on a question or an
   action that hands over the turn.** Never act or speak for {{user}}
   (Prosa, WW+, Nyanko, the user's Narrator).
8. **Field placement:** scenario holds the meeting setup and persistent state
   such as clothing, with no dialogue. Example dialogue teaches style, can be
   framed as an interview, and should never be compressed (Adeline, Aiemm,
   Token Reducer).
9. **Edit discipline:** change only what was asked; keep the original format
   and style (Adeline).
10. **Narrator contract:** be the world, not a character. The turn ends when
    {{user}} must act. State world rules as laws with "no autonomy" clauses. Use
    a passive environment and a slow reveal to control pacing. Write a named
    theme and storytelling philosophy. Give the GM a toolkit of factions,
    antagonists and plot hooks. Force choices between two bad outcomes. Tier
    the cast into primary and secondary (the user's Narrator, statuotw,
    TaleScribe).
11. **Phrase rules positively** to avoid the "no curse" (Yaethi changelog).
12. **Critique mode:** constructive by default, tone set by the user, and every
    point grounded in the actual text with nothing invented (Persona Critic).
13. **NPC capture:** turn characters introduced during play into lorebook
    entries with a fixed sheet (Character Summary).
