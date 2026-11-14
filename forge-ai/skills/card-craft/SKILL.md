---
name: card-craft
description: "Improve a character card's craft: traits that cause observable behavior, a character who fails and resists, example dialogue that trains the right voice and length, and prose without filler. Use for any Reforge that asks to improve, fix, strengthen, or clean up a card."
---

# Card craft

Rewrite a card's fields so every line changes how `{{char}}` replies: traits
with a cause and a visible behavior, a character who can lose and refuse,
example dialogue that teaches the voice, and plain prose.

## Protect

- The instruction sets the scope. Change only the fields and passages it
  asks about, and leave the rest word for word.
- Keep each field's format. Prose stays prose, a PList stays a PList, W++
  stays W++, and headings, brackets, and XML sections keep their shape.
- Keep each field's point of view, tense, and the character's voice:
  vocabulary, sentence length, dialect, and level of profanity.
- Keep every fact: names, ages, relationships, places, abilities, and the
  setting's rules. To fix a contradiction, keep the version the rest of the
  card supports.
- Keep every `{{char}}`, `{{user}}`, and other macro, and every `<START>` line.
- Keep the card's tone and level of explicitness. A craft edit doesn't make
  a card darker, softer, or tamer.
- Build new causes, memories, and tells from what the card already says.
  When the card gives nothing to build from, add one small detail that fits
  it, never a new relative, trauma, or plot.

## Failure modes

- **Adjectives in place of behavior.** The edit swaps one adjective for a
  longer one.
  - Bad: "Kind, loyal, a little shy."
  - Better: "Pays for a stranger's coffee before they reach the till, and
    changes the subject if they thank her."
- **Polishing everything.** The edit rewrites fields the instruction didn't
  name, or smooths every sentence into the same register. Change what was
  asked; leave the owner's sentences alone.
- **Drifting agreeable.** The rewrite softens every edge until the character
  agrees with anything `{{user}}` says.
  - Bad: "Blunt, but always supportive of {{user}}'s choices."
  - Better: "Tells {{user}} a plan is stupid before helping with it, and says
    'I told you' once it fails."
- **Backstory as biography.** The edit expands history the character never
  brings up in a scene.
  - Bad: three paragraphs of childhood, schooling, and first job.
  - Better: one memory the character still acts on (see Causal construction).
- **Settling the arc.** The edit resolves the character's conflict, so
  nothing is left to push against.
  - Bad: "She has finally forgiven her brother."
  - Better: "She sends her brother a birthday card every year and has never
    once mailed it."
- **Writing for `{{user}}`.** Example dialogue or `first_mes` narrates what
  `{{user}}` does or feels. The model then learns to write `{{user}}`'s turn.
- **Cutting example dialogue to save space.** Shorter examples train shorter,
  flatter replies.
- **Cutting the voice with the filler.** A prose pass strips the character's
  idiom along with the ornament. Remove the narrator's padding; keep the
  character's quirks of speech.

## Causal construction

A list of traits gives the model nothing to reason from. A trait with a cause
gives it a chain it can follow into situations the card never mentions.

Write each core trait as **trait → cause → observable behavior**:

- Watchful → was below deck when the ferry *Calloway* flooded and the hatch
  jammed → sits facing the door, and in any room knows where the stairs are.
- Hoards spare parts → the yard laid off half the crew the winter she was
  twenty → never throws out a bolt, and fixes things nobody asked her to fix.

**One shared cause.** Pick the cause that drives two or three traits. State
it in `personality`. Show it in `scenario` as a fact of the present it bears
on, and in at least one `mes_example` block as something the character does
without explaining. A cause stated in one field can drop out of a long chat;
one that shows in three survives.

**One unresolved memory.** Replace a backstory paragraph with one concrete
detail the character still carries: an object, a sound, a sentence someone
said. "She keeps the hatch's bent locking pin in her toolbox." The model can
reuse a concrete detail in a scene; it can only summarize a biography.

**At least one open tension.** Keep one conflict unresolved: two loyalties,
a belief the character hasn't tested, a want they won't admit. A settled
character gives the same reply every turn.

**A physical tell tied to a secret.** Give the secret a visible sign the
character doesn't control. "Turns her wedding ring when anyone mentions the
harbor board." The tell lets the model hint at the secret without stating it.
Put the tell in `personality` or `description` and the secret itself in a
lorebook entry or a line the character never says aloud.

## Testable traits

Every trait must pass one test: **would a reader know it was violated?**
"Guarded" fails, since any reply can be read as guarded. A behavior passes.

| Fails | Passes |
|---|---|
| Guarded | Answers a personal question with a question; tells the truth on the second ask |
| Dry sense of humor | Jokes flat, without a smile, about whatever scares her most |
| Hardworking | Is at the dock before the first shift and leaves after the last |
| Hates authority | Calls every manager by their first name and ignores memos until they're repeated in person |

Keep three to five core traits. More than that and none of them stays
stable.

## Conflict, failure, and preferences

Write each of these into `personality`:

- **How the character fails.** What they do when they lose an argument, get
  something wrong, or are outmatched.
  - Bad: "Confident."
  - Better: "When she's wrong, she goes quiet, fixes it herself that night,
    and never mentions it again."
- **How they handle conflict.** Confront, stall, joke, bargain, or walk
  out, and what finally makes them stop.
- **What they dislike, stated outright.** Models drift toward agreeing with
  `{{user}}`. Name two to four concrete dislikes the character would object
  to in a scene, and what they do when they meet one: "Can't stand being
  thanked in public; leaves the room."
- **Tiered preferences.** Sort likes and dislikes by strength: strongly
  likes, mildly likes, mildly dislikes, can't stand. A flat list makes every
  preference an on-off switch; tiers let the character tolerate some things
  and refuse others.

## Cutting

Ask of every line in `description`, `personality`, and `scenario`: **does
this line ever change a reply? If not, cut it.** These fields are sent every
turn, so dead lines cost tokens and dilute the lines that matter.

- Cut: biography the character never raises, appearance no scene will show,
  an adjective a stated behavior already shows, a rule stated twice.
- Move instead of cutting detail that matters only when its subject comes
  up. Put it in a lorebook entry: `comment` names it, `keys` lists words the
  user or the character will actually say, and `content` states it in a few
  sentences that make sense alone.

For field-by-field runtime behavior (what is sent every turn, once, or
never), load `references/fields.md`.

For prose cleanup with before/after pairs (purple prose, robotic dialogue,
over-description, announced emotion), load `references/unslop.md`.

## Example dialogue

`mes_example` trains voice and reply length more than any description does.

- Open each block with a short `{{user}}` line: a question, a remark, or one
  brief action. Then write the character's reaction. The `{{user}}` line is a
  prompt; it never narrates what `{{user}}` does next, feels, or decides.
- Write the character's reply at the length replies should be. Long examples
  train long replies; short, dialogue-heavy ones train conversation.
- Vary verbs and openings across blocks. When three blocks start with the
  character smiling, the model starts every reply with a smile.
- Cover different pressures: a calm moment, a conflict, and a moment the
  character wants something. Show the shared cause and the failure behavior
  in at least one block.
- Make each block stand alone. The app drops blocks as the chat grows.
- Keep the card's example format: `{{char}}:` prefixes, asterisks or quotes,
  and the `<START>` lines.

For three before/after rewrites, load `references/examples.md`.

## Scenario and first message

- `scenario` holds what stays true for the whole chat: the setting, how the
  character knows `{{user}}`, and the standing stakes. It is sent every turn,
  so a moment written there ("{{char}} waits at the bar") keeps coming back
  long after the scene moves on.
- The current moment belongs in `first_mes`, which is sent once: where the
  character is, what is happening this minute, and the hook `{{user}}`
  answers.
- If the instruction covers both fields, move the moment from `scenario` to
  `first_mes`. If it covers only `scenario`, rewrite the moment as the
  standing situation ("{{char}} works the bar most nights").

## Edits per field

| Field | Edit |
|---|---|
| `description` | Role, look, and competence as things a scene can show. Replace backstory paragraphs with the one unresolved memory. Put the physical tell here if the card describes appearance here. |
| `personality` | Three to five testable traits, each with its cause. The shared cause stated once. Failure behavior, conflict style, outright dislikes, tiered preferences. |
| `scenario` | The standing situation and stakes, and the shared cause as a fact of the present. Move the current moment out to `first_mes` when both fields are in scope. |
| `first_mes` | The current moment, shown through the character acting, at the length replies should be. End on something `{{user}}` must answer. |
| `mes_example` | Short `{{user}}` prompt, then the character's reaction. Varied verbs, the target length, the shared cause shown in one block. See `references/examples.md`. |
| `alternate_greetings` | Same rules as `first_mes`. Each greeting stays consistent with the traits and causes you wrote. |
| `system_prompt` | Leave it unless the instruction names it. Character traits belong in `personality`, not here. |
| `post_history_instructions` | Leave it unless the instruction names it. If the edit adds a behavior the character keeps losing in long chats, a one-line reminder can go here. |
| lorebook entries | Detail moved out of the permanent fields: one subject per entry, `keys` the chat will actually contain, `content` that stands alone. Use `constant` only for a fact every reply needs. |
| `creator_notes` | Update what it says about the character so it stays true. Keep credits, links, and play notes. |

## Done when

- Every core trait in `personality` names a behavior a reader could catch
  being broken.
- Each core trait traces to a cause, and one cause shows in `personality`,
  `scenario`, and at least one `mes_example` block.
- The card holds one concrete unresolved memory and at least one open
  tension.
- `personality` says how the character fails, how they handle conflict, and
  at least two things they dislike.
- No line in `description`, `personality`, or `scenario` could be cut
  without changing some reply.
- No `{{user}}` line in `mes_example` or `first_mes` narrates `{{user}}`'s
  actions or feelings, and no two example blocks open with the same verb.
- `scenario` describes a situation, not a single moment.
- Fields the instruction didn't name are unchanged, and edited fields keep
  their format and point of view.

## Always

- Never write `{{user}}`'s actions, words, or thoughts, in any field.
- State rules as what to do. Naming a behavior to forbid it steers the model toward it.
- Never compress or cut example dialogue to save tokens; it sets the character's voice.
- Bring `creator_notes` in line with the edit; keep its credits and links.
