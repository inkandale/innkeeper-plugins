# Research: craft guidance for roleplay character cards and scenes

Purpose: source material for Agent Skills that edit SillyTavern-style cards.
Each item is tagged with how well it is supported:

- **[Docs]**: documented platform behavior (SillyTavern docs, the V2 card spec). Treat it as fact.
- **[Consensus]**: several independent community guides agree on it and give a rationale. Strong practitioner evidence, but no controlled tests.
- **[Craft canon]**: established fiction or game-mastering technique with a named originator and wide adoption.
- **[Opinion]**: one author's view, or promotional content.

No source I found has controlled tests of card formats or techniques. Even the
consensus items rest on practitioner reports.

---

## 1. The two requested articles

### 1a. MiniTavern blog: "How to Write SillyTavern Character Card Descriptions That Drive Engagement"
URL: https://blog.mini-tavern.com/blog/how-to-write-sillytavern-character-card-descriptions-that-drive-engagement-and-r-8c3568
Promotional (MiniTavern apps and market). No evidence is given. **[Opinion]** throughout.

Rules it gives:
- Five components for a description: 3–5 specific core traits (not "nice"/"mean"); backstory hooks that leave mystery; motivations and goals ("what does the character want?"); voice and speech patterns; emotional range (how they react under stress, joy, sorrow).
- Six steps:
  1. Open with a vivid image or emotional detail, not "Elena is a mage."
  2. Show traits through behavior, e.g. "deflects personal questions with a playful smile and a topic change", not "Elena is secretive."
  3. Plant backstory breadcrumbs that the user can follow or subvert.
  4. Specify speech and mannerisms, with sample phrases if useful.
  5. Add emotional triggers: concrete stimulus → reaction. Example: "a child laughing reminds her of her lost sister → melancholy".
  6. 200–400 words, every sentence pulling weight. The range is given with no rationale.
- Mistakes: over-explaining (kills mystery); contradictory traits ("always cheerful" plus a tragic backstory); ignoring the user's role (write invitations such as "She seems to trust you, but only time will tell"); unbroken darkness (balance it with warmth or humor).
- Worked example: Elena. The useful idea in it is a **cost mechanic as a ticking clock**: "her magic costs her memories one at a time". A **physical tell tied to a secret**: "her hands tremble at the word 'crystal'".
- Testing: ask the character about its past, its goals, and a random event, then check the replies against the card.

Worth keeping: emotional triggers written as stimulus→reaction pairs, physical tells for secrets, and a cost or ticking-clock mechanic. The rest is generic.

### 1b. TavernSprite: "SillyTavern Character Card Best Practices (2026)"
URL: https://tavernsprite.com/blog/sillytavern-character-card-best-practices/
Promotional (an expression-pack generator). **[Opinion]**, but most points match the consensus below.

- Core principle: "every token should earn its place". Be specific, consistent, and lean.
- Description: write a character sheet, not a short story. Cover age range, role, defining traits, speech style, key relationships, and what they want. Cut backstory that never surfaces in play. Example: "Curt, sarcastic, secretly loyal." Aim for "a few hundred" tokens.
- Personality: list traits and behaviors, not prose ("Deflects with jokes when uncomfortable"). Cover how they speak, how they react under pressure, what they value, and what they avoid. "Each line should be actionable in the next reply."
- First message: the "highest-leverage field". The model copies its sentence length, vocabulary, amount of narration, and how it treats the user. **[Consensus; SillyTavern docs agree, see 2a]**
- Use `{{char}}` and `{{user}}`, never hard-coded names.
- Example dialogue: 2–3 exchanges to lock the voice.
- A bloated card pushes recent messages out of context, which is why characters "forget". Delete any detail that never changes a response.
- Common mistakes: contradictions ("shy and reserved" + "loves the spotlight"), walls of unused backstory, generic greetings, hard-coded names, no example dialogue.

Worth keeping: the deletion test ("does this line ever change a reply? If not, cut it") and "each personality line must be actionable in the next reply."

---

## 2. Community card-writing guides

### 2a. SillyTavern docs: Character Design **[Docs]**
URL: https://docs.sillytavern.app/usage/core-concepts/characterdesign/
- Permanent fields, sent on every request: name, description, personality, scenario. Put critical facts here.
- First message: sent once. "The model is more likely to pick up the style and length constraints from the first message than anything else."
- Alternate greetings: shown as extra swipes of the first message. Group chats pick one at random.
- Example dialogue: `<START>` separates blocks; use `{{char}}:` and `{{user}}:` prefixes. Blocks are included only while context has room and are dropped one at a time as the chat grows, unless forced to stay.
- `system_prompt` and `post_history_instructions` are used only when the user enables "Prefer Char. Prompt/Instructions". `{{original}}` inserts the user's own prompt.
- Creator's notes are never sent to the model.
- Character's Note (depth prompt) is injected at a fixed depth (0 means after the last message) with a chosen role. Use it to reinforce traits as the chat grows.
- Budget example: a 1000-token card in a 2048-token context leaves about 3 exchanges of memory. The token counter turns red past half the context.

### 2b. Character Card V2 spec **[Docs]**
URL: https://github.com/malfoyslastname/character-card-spec-v2/blob/main/spec_v2.md
- `system_prompt` and `post_history_instructions` replace the user's settings by default and must support `{{original}}`.
- `creator_notes` must not go into prompts. Show at least one paragraph of it to users.
- `alternate_greetings` are extra swipes.
- `character_book` stacks with the user's world book and takes precedence over it.
- Do not use `tags`, `creator`, or `character_version` for prompt engineering.
- Editors must preserve unknown `extensions` keys.

### 2c. AliCat, "Ali:Chat" v1.5. https://rentry.co/alichat **[Consensus: widely cited, the base of Trappu and kingbri]**
Core idea: express traits through example dialogue and actions instead of trait lists. "Interesting dialogue produces interesting output."
- Every `{{char}}` line has a `{{user}}` line before it. A short user line followed by the bot's reaction teaches the model to answer similar input the same way. Example: `{{user}}: *pats head*` / `{{char}}: *purrs*`.
- Put the character's name in actions every 2–3 sentences so the lines bind to the character.
- **Vary verbs across examples.** Repeating "smiles" in several examples causes looping. Use synonyms.
- Keep examples few and open-ended. Never define the character with a single example.
- If you want long replies, write long examples. The greeting length drives reply length.
- The last example should overlap the greeting so the voice stays consistent.
- Interview format (Q&A) is fine for drafting. Convert it into a scene once it has done its job.
- Placement strength: top of the description is weakest and bottom is strongest. The greeting is the strongest influence on scene setup.
- If the character drifts late in a chat, reinforce traits or move them closer to the end of the context.
- Keep confusing relationship details out of World Info.

### 2d. Trappu, PLists + Ali:Chat guide **[Consensus: linked from the SillyTavern docs]**
URL: https://wikia.schneedc.com/bot-creation/trappu/creation (the page is JS-heavy; fetch the raw HTML)
- **Name bias:** a name carries information. "Caera Denoir" reads as noble-born. Adding a title or descriptor conveys backstory cheaply. Sharing a name with a famous character pulls in that character.
- **Context strength:** the lowest text in the context influences the reply most. The description sinks into "basket 3" (weak) as the chat grows. That is why the PList belongs in the **Character's Note at depth 4**, where it keeps "pulling" the Ali:Chat examples back into relevance.
- **Character's Note depth guide:** 0 for hard instructions (e.g. `[Instruction: {{char}}'s next response must include narration and dialogue]`); 4–8 for a PList or guidance (4 if unsure); 9+ for a slight nudge such as a scenario or minor point.
- **PList format:** `[{{char}}'s appearance: hair(light brown), ...; Tags: comedy, fantasy; {{char}}'s persona: nimble, cheeky, acts stupid when confronted, ...]`. Put less important categories first and the most important last.
- **Tags as "hints":** genre and theme keywords inside the PList steer the scenario without forcing it ("the impact of tags is extremely underrated").
- "You put into the card what you want out of the card." If the card is for chatting, the examples should be chatting. If it is for adventure, they should be adventure.
- Reply length: a few long examples give long replies; many short dialogue-heavy examples give conversational replies; very short lines with no actions give a chat-room style. Put examples of the length you want nearest the bottom.
- Every trait reinforced in an example must also appear in the PList.
- Plant hints of possible plot directions inside examples to make the card more versatile.
- **Impersonation:** never narrate the user's actions in the greeting or the examples, or the model learns to do it.
- **Greeting:** carries the most weight at the start. It sets the scenario, the relationship, the tone, and the length. It is temporary, so it can be long.
- One or two well-written examples of about 150 tokens beat many weak ones. Move extra examples (clothes, lore) into the lorebook.
- "Lazy 0-shot scenario" option: no greeting, plus a bracket such as `[Scene: X is robbing {{user}}'s house; Tags: robbery, chase, betrayal]`.

### 2e. kingbri, "MinimALIstic (Ali:Chat Lite)" https://rentry.org/kingbri-chara-guide **[Consensus/Opinion]**
- The card holds only the Ali:Chat examples. The PList goes in the Character Author's Note.
- Use one PList with sections separated by `;`. Several arrays raise the risk of "bracket leakage" into output.
- Label the main section "{{char}}'s persona", not "Character".
- Budget: examples about 300–600 tokens, whole card under about 600 permanent tokens. This advice predates large contexts and suits small local models.
- Examples: 3 × 5–6 lines or 2 × 7–8 lines, with prompts like "Appearance?", "Personality?", "Brief life story?". Use the name at least once per example; heavy repetition pushes some models into third person.
- **Greeting "flipped scenario" technique:** write the greeting in second person as if you were the user, then swap roles so the character does those things and the user is only `{{user}}`. Never describe the user's actions.
- Remove leading questions from the greeting if the bot starts looping questions.
- Lorebook: key entries with associations (`BDIT,Bluudale Institute of Technology,college`). For lore, pair a PList fact with an Ali:Chat line showing the character's reaction. Use Selective with secondary keys to scope an entry to a character.

### 2f. Sukino, "Guides" https://rentry.org/Sukino-Guides **[Consensus]**
- **Wording sets style.** "Reginald contemplates" produces a different voice from "Reginald always ponders the ramifications of his actions."
- **Don't name what you don't want.** A negated mention still primes the concept. Saying a character isn't interested in sex still pulls in erotic material. (Statuo independently says to prefer positive instructions.)
- Don't paste wiki pages. The model tries to use everything it is given.
- Permanent traits go in permanent fields. The *current situation* goes in the greeting or examples, because Scenario text keeps resurfacing for the whole chat.
- Never write the user's actions or dialogue in the greeting or examples.
- Use lorebooks for conditional information.
- Don't override the system prompt in a shared card unless you must.
- Delimit fields with XML tags or brackets so lorebook text doesn't blend into them.
- First-person defs give a stronger voice. Small models need traits stated explicitly.
- Randomness: `{{pick}}` is stable for the whole chat, `{{random}}` rerolls every turn, `{{roll}}` rolls dice.

### 2g. "Advanced card writing tricks" https://rentry.org/AdvancedCardWritingTricks **[Consensus/Opinion: examples are real cards]**
The most technique-dense source found. Relevant items:
- **Alternate greetings:** different setups, each summarized in creator_notes. The main defs and examples must fit *every* greeting. The first greeting carries all key character info; later ones can assume it.
- **Status line ("statistics"):** require a formatted data line in every reply. Examples: distance remaining to a destination gives an ending condition and pacing; current location forces real location changes; an arousal % makes escalation gradual instead of instant; a numbered diary entry where a lorebook keyed to a future number triggers a plot change.
- **Inner thoughts** in brackets or italics give the private-vs-public contrast.
- **Hidden text** `[](#'...')` / `<!-- -->`: situational instructions inside the greeting (they disappear when the greeting scrolls out); a secret such as the culprit's identity (risky, it leaks); or **a lorebook keyword only**, so the secret lives in a lorebook entry and never appears in visible text.
- **Pregreeting examples:** write example dialogue set *before* the greeting so the model treats it as history and doesn't copy it. For several greetings, make it distant ("And then, 18 years pass.").
- **Ephemeral instructions** in examples: `{{user}}: [System: ...]`. They fall out of context naturally.
- **Post-history instructions:** use them when the defs aren't followed. Put `{{original}}` *after* the card's text so the user's jailbreak keeps priority.
- Depth prompt: stronger than the defs, weaker than post-history.
- System prompt: users often disable it, so keep crucial info in the defs. Use it for non-RP formats (terminal, diary, forum thread) or narrator cards.
- **In-character defs** (first person, diary, the character's own style) suit strongly stylized voices.
- `{{random}}` in the greeting rolls once at chat start, e.g. pick the culprit at random. In the defs it rerolls every turn, which varies behavior.
- `{{idle_duration}}` / `{{time}}`: a character reacts to reply delay or the time of day.
- XML-tagged sections referenced by name in instructions ("follow story_flow").
- Multi-stage stories are built from lorebook-driven progression. Cited cards to study: "Impossible To Touch" and "The Rite of Belwick".

### 2h. Statuo, "Getting More Out of Your Bot Chats" https://rentry.org/statuotwtips **[Consensus]**
User-side, but it carries over to cards:
- Don't define `{{user}}`'s thoughts or backstory on the card.
- Use positive instructions ("do X", not "don't Y").
- End each message with something to act on: a question, an event, a hook. An open ending makes the model fill the gap, often by writing for the user. **A greeting should end on a hook directed at `{{user}}`.**
- Time-skip markers ("THE NEXT DAY") give clean scene breaks. Use them sparingly or the model copies them.
- Keep about 2 active characters at once; the model won't bring in established characters by itself.
- System prompts: open with the objective. "You are {{char}}" reduces bleed but blocks NPCs; a narrator framing allows NPCs but raises the risk of the model writing for the user.

### 2i. Format debate (W++ vs PList vs prose) **[Consensus on direction; no tests]**
Sources: Reddit threads in the search results above; the SopakcoSauce PList docs https://sopakcosauce.gitbook.io/sopakcosauce-docs/plist-sbf-guides
- W++ is broadly considered token-inefficient and no better than a PList.
- PLists are compact. Large API models handle plain prose fine.
- The repeated conclusion: content and relevance matter more than format. For skills targeting modern large models, **prose or light structure plus strong examples** is the safe default. PLists are a token optimization for small contexts.

---

## 3. Lorebook mechanics (the basis for reveal and progression patterns)

### SillyTavern World Info docs **[Docs]**
https://docs.sillytavern.app/usage/core-concepts/worldinfo/
- Keys: comma-separated and case-insensitive by default. `/regex/` is supported. Speaker names are prefixed to lines, so `/\x01{{user}}:[^\x01]*?hello/` matches only what the user said.
- Secondary keys with AND ANY / AND ALL / NOT ANY / NOT ALL.
- Strategies: constant, selective (keyword), vectorized.
- Positions: before or after char defs ("after" is stronger), before or after examples, top or bottom of Author's Note, @Depth with a role, outlet (`{{outlet::Name}}`).
- Higher order sits closer to the end of the prompt and has more influence.
- Probability per entry (random events).
- Recursion: entries trigger other entries by mentioning their keys. Flags: non-recursable, prevent further recursion, delay until recursion (with levels).
- **Timed effects (in messages): Sticky** keeps an entry active N messages; **Cooldown** blocks reactivation for N messages; **Delay** blocks activation until the chat has N messages. All three reset on swipe or delete.
- **Inclusion groups:** only one entry per group is inserted, chosen by weight, order, or key-match score.
- Content must stand alone (keys and titles aren't inserted), stay concise, and name related entries so recursion pulls them in. "Entries guide the model but do not guarantee the output."

### Community extensions of these mechanics **[Consensus/Opinion]**
- Inclusion groups give mutually exclusive states: a default "concealed" entry and a "revealed" entry. A very high sticky value makes a reveal permanent for the chat. (Claude-generated artifact cited in search results; treat with caution.)
- Sticky plus cooldown makes a rhythm in which lore surfaces and then goes dormant.
- Event entries need a cooldown so they don't fire every turn.
- Advanced Tricks: a lorebook entry arrives *one message late*. The character raises a topic, and the entry's information shows up in the next reply. Choose keywords the user or character will actually say, or make the defs prompt the character to raise the topic.
- Lorebook Compendium (https://aceeenvw.github.io/lorebook-compendium/, no license stated): write entries as "how the character behaves now", not as history; isolate secrets and explicit material so they don't fire in ordinary scenes; avoid broad keys.

---

## 4. Scene craft that an LLM can apply

### 4a. Scene structure (general)
- **Scene/Sequel and MRUs** (Dwight Swain; Randy Ingermanson's summary) **[Craft canon]**
  https://www.advancedfictionwriting.com/articles/writing-the-perfect-scene/
  - Scene = Goal → Conflict → Disaster. "Winning is boring": the scene should not end in a clean win.
  - Sequel = Reaction → Dilemma → Decision. The decision sets the next goal.
  - MRU: the motivation is objective and external, in its own paragraph. The reaction comes in the fixed order **feeling → reflex → rational action/speech**. You can drop parts but must keep the order.
  - RP mapping: each `{{char}}` reply is a reaction to the user's motivation. Reaction order gives the reply its internal shape. Ending a reply on a disaster or complication hands the user a sequel to play.
- **Try-fail cycles: "yes, but" / "no, and"** **[Craft canon: Writing Excuses (Sanderson, Kowal), improv; from memory, not fetched]**. An attempt's result should complicate things rather than resolve them. The Marinara preset's rule "every action is but an attempt" is the RP version.
- **MICE quotient** (Orson Scott Card; extended by Mary Robinette Kowal) **[Craft canon; from memory, the Kowal page 404'd]**. Threads are Milieu (enter or leave a place), Inquiry/Idea (a question raised and answered), Character (an internal dissatisfaction resolved), Event (the status quo disrupted and restored). Close threads in reverse order of opening. RP use: label what kind of thread a card or greeting opens, so the model knows what "resolution" means.
- **Character arc: Lie / Truth / Want / Need / Ghost** (K.M. Weiland) **[Craft canon]**
  https://www.helpingwritersbecomeauthors.com/write-character-arcs/
  The Lie is a false belief that drives the flaw. The Want is the external goal; the Need is the truth they must face. The Ghost is the past wound that explains the Lie. Arcs can be positive, flat, or negative. The structure beats are the characteristic moment (the Lie in action), first plot point (the Lie stops working), midpoint (revelation), third plot point (low point), climax (the Truth tested).
- **haowjy/creative-writing-skills scene-construction** (Apache-2.0, see section 5):
  - Enter mid-action.
  - Every line of dialogue does two jobs.
  - Subtext: characters deflect or answer a different question.
  - Speakers are identifiable without tags.
  - Prefer action beats to tags.
  - Alternate high- and low-tension beats ("sustained intensity becomes numbing").
  - End on forward momentum.
- **Prose (same repo):**
  - Psychic distance (John Gardner): move close for peaks and pull back for transitions. Default AI prose sits at a flat middle distance.
  - Free indirect discourse.
  - Vary sentence rhythm.
  - Use one or two sensory details filtered through what this character would notice (a chef notices smells, a soldier notices exits), not a five-senses catalog.
  - Interiority contracts in action and expands in reflection.
  - Show non-POV characters through observable behavior ("her jaw tightened").

### 4b. Combat and action
Fonda Lee via Writer's Digest **[Craft canon-ish: professional author, consistent with the other guides]**
https://www.writersdigest.com/whats-new/blow-by-blow-writing-action-and-fight-scenes
- A fight must change something irreversibly. If cutting it changes nothing, cut it.
- Establish who is fighting and why before the fight.
- Choose an emotional effect (desperation, humiliation, loyalty) rather than a blow-by-blow account.
- Vary distance: inside the head, then pulled back.
- Short choppy sentences speed time up; dwelling slows it down.
- Realism: untrained people lose to trained ones; skilled fighters still get hit; fights are short and exhausting; injuries persist.
Other guides (Jericho Writers, Well-Storied): fights intensify and taper; over-description stalls them; focus on the few impactful moments.
RP-specific additions from the Marinara preset and GM practice **[Consensus]**: the user's action is an *attempt* whose outcome the narrator decides. Telegraph a threat before landing it (see the Apocalypse World moves in 4g). End each combat beat on a new choice for the user.

### 4c. Romance and slow burn
- **Romancing the Beat** (Gwen Hayes) **[Craft canon for the romance genre]**, via https://plottr.com/romancing-the-beat-template/. Four phases with 20 beats:
  - Setup: introduce H1 and H2, meet cute, No Way 1, adhesion.
  - Falling: No Way 2, inkling of desire, deepening desire, "maybe this could work", midpoint of love (a false high).
  - Retreating: inkling of doubt, deepening doubt, retreat beat, shields up, break up.
  - Fighting for love: dark night, wake up, grand gesture, what whole-hearted looks like, epilogue.
  For RP this works as a *relationship stage ladder* in a lorebook or depth prompt. The card states which phase the character is in and what moves it to the next.
- **Slow burn** (Writer's Digest, "5 Keys", https://www.writersdigest.com/write-better-fiction/5-keys-to-writing-a-slow-burn-romance) **[Consensus]**:
  - Near-misses: interrupted almost-kisses, a hug that lasts too long.
  - Small gestures between big beats.
  - Pining from both sides.
  - An external plot that keeps moving.
  - A payoff that is worth the wait.
  Other blogs add a **touch escalation ladder**: accidental touch → necessary touch → pretext touch → lingering touch → almost-kiss → kiss. "Each beat must be escalation, not repetition." **[Opinion, blog-level, but coherent]**
- **haowjy romance addendum:** attraction grounded in specifics; subtext over declaration; intimacy should cost something. LLM failure modes: announcing chemistry instead of showing it, jumping from tension to confession too fast, conflicts that dissolve with one honest sentence, both voices polished into the same fluent register.

### 4d. Intimate scenes (craft level)
- Jo McNally via Writer's Digest **[Craft canon-ish]**
  https://writersdigest.com/write-better-fiction/why-is-this-love-scene-here-how-to-write-compelling-love-scenes
  - The scene must change something emotionally. "They have sex" is not a reason for it to exist.
  - Start from the emotional stakes: who has the most to gain or lose.
  - Decide each character's emotional state (nervous, tender, angry), the place, and the time before writing.
  - Pace with prose: frantic means short sentences and urgency; slow means long sentences and deliberate movement. Intensity may shift mid-scene.
  - Tailor the scene to the characters: an anxious character gets slower pacing and a partner who checks in. Humor is allowed.
  - Fade to black is valid.
- Consent as craft **[Consensus among romance authors]**: https://jezebel.com/the-romance-novelists-guide-to-hot-consent-1822991922, https://countingstarsheep.com/tips-on-how-to-purposefully-write-consent-into-your-romance-novel, Roni Loren's course notes, Harlequin's guidance.
  - Show the presence of a yes, not just the absence of a no.
  - The pause before an answer is a tension tool.
  - The partner can't read minds, so signals must be on the page: verbal check-ins and visible reciprocal enthusiasm.
  - Kink does not exempt a scene from consent.
  - Word-choice warning signs: "loom", "claim", a threatening tone.
- RP-specific: a status line with a percentage (Advanced Tricks) prevents jumping from zero to everything. Statuo and Sukino warn that naming explicit content anywhere in a card primes it, so keep it in gated lorebook entries (Compendium).

### 4e. Horror and tension
- **Hitchcock's bomb under the table** **[Craft canon]**: surprise gives about 15 seconds of shock, suspense gives about 15 minutes of tension. Suspense comes from the audience knowing more than the characters. Bordwell's caveat: Hitchcock himself mixed the two.
  https://nofilmschool.com/alfred-hitchcock-and-francois-truffaut-explain-surprise-vs-suspense, https://www.davidbordwell.net/blog/category/narrative-suspense/page/5/
  RP use: let `{{user}}` notice the threat before the characters do, or let the character know something `{{user}}` doesn't and leak it through tells.
- **haowjy horror addendum:**
  - Protect unknowns.
  - Use sensory *wrongness*: small violations before any reveal.
  - Let anticipation do the work.
  - Show denial and delayed recognition.
  LLM failure modes: explaining the threat too early, naming fear instead of showing perception, escalating every beat until it becomes noise, gothic adjectives in place of concrete wrongness.

### 4f. Mystery and investigation
- **Three Clue Rule** (Justin Alexander) **[Craft canon in GM practice]**
  https://thealexandrian.net/wordpress/1118/roleplaying-games/three-clue-rule
  - Every conclusion needs at least three clues: players miss one, ignore one, and misread one.
  - Permissive clue-finding: reward clever approaches with information.
  - Proactive clues: if players stall, "a guy with a gun walks through the door" (Chandler), and each event yields new clues.
  - Have a last-resort event ready.
  - Use few red herrings; players invent their own.
  - Keep a revelation checklist and mark a conclusion done only when it is actually reached.
  RP use: a mystery card's lorebook should hold the solution plus three or more independently keyed clues, each reachable by a different action.
- **haowjy mystery addendum:**
  - Clues should be visible but not highlighted.
  - Red herrings come from plausible motives.
  - Track who knows what.
  LLM failure modes: clues that glow, solving with withheld information, over-explaining deductions immediately.
- Advanced Tricks: choose the culprit with `{{random}}` in the greeting, and keep the identity in a lorebook keyed by a hidden keyword.

### 4g. Game-master principles (the strongest source for "drive the scene")
**Apocalypse World MC moves** (Vincent Baker) **[Craft canon; moves fetched, principles from memory]**
https://daily-apocalypse.com/daily-apocalypse/50-mc-moves
- Moves:
  1. Separate them.
  2. Capture someone.
  3. Put someone in a spot.
  4. Trade harm for harm.
  5. Announce off-screen badness.
  6. Announce future badness.
  7. Inflict harm.
  8. Take away their stuff.
  9. Make them buy.
  10. Activate their stuff's downside.
  11. Tell them the possible consequences and ask.
  12. Offer an opportunity, with or without a cost.
  13. Turn their move back on them.
  14. Make a threat move.
- After every move, ask "what do you do?"
- Agenda: make the world seem real; make the characters' lives not boring; play to find out what happens.
- Principles (from memory):
  - Address the characters, not the players.
  - Make your move but misdirect.
  - Name everyone and make everyone human.
  - Ask provocative questions and build on the answers.
  - Be a fan of the players' characters.
  - Think off-screen too.
  - Sometimes disclaim decision-making.
- Soft vs hard moves (community usage, https://daily-apocalypse.com/daily-apocalypse/90-making-your-moves-as-hard-and-direct-as-you-like): soft moves telegraph or escalate; hard moves deliver irrevocable consequences, usually after a soft move is ignored.
- This maps directly onto a "narrator" or post-history instruction: *each reply makes one move, then hands the user a choice.*
- (Not fetched) Blades in the Dark progress clocks: a 4/6/8-segment clock per threat, ticked on consequences. This is the template for status-line escalation.

### 4h. Slice of life
No strong craft source found. The best available: Scene/Sequel still applies at a small scale (a small want, a small obstacle), alternate tension levels (haowjy), and the Statuo and Trappu point that **examples should match the intended mode** (a chatty card needs chatty examples). Gap: no researched source on kishōtenketsu or low-stakes structure. Flag it as an area where the skill will rely on judgment.

---

## 5. Existing skills and presets

| Source | What it encodes | License |
|---|---|---|
| haowjy/creative-writing-skills (585★) https://github.com/haowjy/creative-writing-skills | Agent Skills plugin. `creative-writing-craft` SKILL.md is a thin router to resources (prose-writing, scene-construction, genre/{romance,horror,mystery,thriller,fantasy,litfic}). `character-sim`: speak from the character's *knowledge*, not the story's; under pressure people stall, deflect, joke, over-explain; "they rarely name their inner state cleanly." Genre files follow a "Protect / Common LLM Failure Modes / During Revision" template, **a good format to copy**. | Apache-2.0 |
| Marinara's Spaghetti Recipe (Universal Preset) https://huggingface.co/MarinaraSpaghetti/SillyTavern-Settings | Narrative rules: respect player agency (never write `{{user}}`'s lines; recount them as "{{user}} asked for directions"); "every action is but an attempt", keep it challenging, absurd attempts fail; embody every NPC with a distinct voice, motivations, flaws, spatial awareness; actively develop the story with challenges, twists, emotional beats; vary length by scene (one line in quick exchanges, 2–3 paragraphs for a new location); match the user's POV. Also contains an NSFW jailbreak block. **Don't copy that.** | No license file found; treat as all-rights-reserved and paraphrase only |
| PixiJB https://rentry.co/znon7vxe (listing) | Built for Claude 3: plain English plus shallow XML following Anthropic's prompting guide, no all-caps or symbol tricks. Contents not retrieved. A jailbreak, so don't copy. | None stated |
| NemoEngine https://github.com/NemoVonNirgend/NemoEngine | Large modular preset. Users report small models ignore its rules. Not inspected. | Not checked |
| Microcot https://github.com/aimicrocot/microcot | CoT preset (credits Celia for an HTML regex). The "Celia" preset was not found. | Not checked |
| Other "story-writer" / "creative-writer" skills on skills.sh and openskillindex | Generic. Some openclaw entries are test fixtures. Not worth adopting. | Various |

No dedicated public *roleplay card editing* skill was found. That gap is what the planned skills would fill.

---

## 6. Patterns for steering a card (better than tone adjectives)

Each pattern names the card field it lives in.

1. **Want / Need / Lie / Ghost** (Weiland) in the description. Direction becomes "which way the Lie bends under pressure", not "be darker". Shifting the tone means changing the Lie or the Want, not the adjectives. **[Craft canon]**
2. **Stimulus → reaction triggers** in the description or personality ("seeing a child laugh → melancholy"), plus **physical tells for secrets** ("hands tremble at 'crystal'"). They are concrete and actionable on the next reply. **[Opinion, 1a; but matches "actionable lines" in 1b and show-don't-tell canon]**
3. **Hidden motive with gated reveal:** a public-facing behavior in the description, the true motive in a lorebook entry in an inclusion group (concealed vs revealed), the reveal triggered by keywords and secondary-key logic, made permanent with a high sticky value. Use Delay so it can't fire before N messages. **[Docs mechanics + community pattern]**
4. **Escalation ladder or clock:** a status line in every reply (distance remaining, suspicion %, relationship stage, clock segments). Lorebook entries keyed to thresholds change behavior. **[Consensus: Advanced Tricks + Blades/AW canon]**
5. **Relationship stage ladder** (Romancing the Beat phases or a custom ladder) in a depth prompt or lorebook. Each stage states allowed behaviors and the trigger to advance. **[Craft canon + community]**
6. **Alternate greetings as entry points:** each a different scene (Scene/Sequel moment, MICE thread type, genre), all compatible with the shared defs. The first carries the full character info. Each is summarized in creator_notes. Each ends on a hook toward `{{user}}` and never narrates `{{user}}`. **[Consensus]**
7. **Examples as behavioral training:** examples show *reactions to user moves* under pressure (Ali:Chat key/response), vary verbs, and are placed by desired length. Use "pregreeting" history so the model doesn't copy them. A tone shift is done by rewriting the examples, not the adjectives. **[Consensus]**
8. **Post-history and depth prompt as the GM layer:** one AW-style move per reply, end with a choice; three-clue discipline for mysteries; suspense over surprise for horror. Put `{{original}}` *after* the card's text. Keep critical facts in the defs, since users may disable these fields. **[Consensus + craft canon]**
9. **Randomized setups:** `{{random}}` in the greeting (culprit, mood, starting event) rolls once. Weight options by repeating them. **[Docs-level macro behavior + community]**
10. **Tags as hints:** genre and theme keywords in the PList or description bias the model toward introducing matching elements without forcing it. **[Opinion, Trappu; plausible]**

## 7. Anti-patterns (cross-source, mostly well supported)
- Narrating `{{user}}`'s actions, dialogue, or thoughts in the greeting or examples teaches impersonation. (Trappu, kingbri, Sukino, Statuo, Marinara. **Strongest consensus in the whole corpus.**)
- Contradictory traits. (1a, 1b)
- Backstory dumps and wiki pastes that never affect replies. (1b, Sukino)
- Naming unwanted content, even negated. (Sukino; Statuo's "positive instructions")
- Repeated verbs or patterns across examples cause loops. (AliCat, kingbri)
- Leading questions in the greeting can loop. (kingbri)
- The current situation written in Scenario keeps resurfacing; put it in the greeting. (Sukino)
- Hard-coded names instead of `{{char}}` and `{{user}}`. (1b, the docs)
- Overriding `system_prompt` in a shared card without need, or omitting `{{original}}`. (Sukino, spec)
- LLM prose failure modes: middle psychic distance, announced emotions, escalation without relief, explained threats, glowing clues, instant confessions. (haowjy)

## Gaps and caveats
- Not fetched: the Kowal MICE page (404), Apocalypse World principles (stated from memory), Blades in the Dark clocks (from memory), PixiJB contents, NemoEngine, and the Celia preset (not found).
- The token budgets (kingbri's 600, MiniTavern's 200–400 words) come from small-context, older-model eras. Modern 100k+ contexts change the trade-off, but "irrelevant detail dilutes" still holds.
- No controlled comparisons of card formats exist in public material I found.
