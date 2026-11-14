# What each field does at runtime

How SillyTavern and apps that follow the Character Card V2 spec use each
field. Where a line goes decides how often the model sees it and how much it
weighs.

| Field | When the model sees it | What follows for an edit |
|---|---|---|
| `description` | Every turn, near the top of the prompt | Permanent cost. Its weight fades as the chat grows and newer text sits closer to the reply. Put facts here that must hold all chat long. |
| `personality` | Every turn | Permanent cost. The place for traits, causes, and failure behavior, since it never drops out. |
| `scenario` | Every turn | Permanent cost, and whatever it says keeps resurfacing. Write the standing situation, not a moment. |
| `first_mes` | Once, as the opening message | The strongest early influence on reply length, sentence style, amount of narration, and how the character treats `{{user}}`. It scrolls out of context in a long chat. |
| `alternate_greetings` | Once, if chosen: each is another swipe of the first message | Same weight as `first_mes`. Group chats pick one at random. The permanent fields must fit every greeting. |
| `mes_example` | While the context has room; blocks split by `<START>` drop one at a time as the chat grows | Trains voice and length early, then fades. Never keep a rule only here. Each block must work alone. |
| `system_prompt` | Only when the user enables "Prefer Char. Prompt"; it then replaces the user's own system prompt | Many users leave it off, so the character must work without it. `{{original}}` inserts the user's prompt. |
| `post_history_instructions` | Only when the user enables "Prefer Char. Instructions"; sent after the chat history | The last text before the reply, so the strongest position. Same opt-in caveat as `system_prompt`; `{{original}}` keeps the user's own instructions. |
| `creator_notes` | Never; the app shows it to the player | Free of token cost, but a rule for the model written here never reaches it. |
| `tags` | Never as instructions | Labels for search, not steering. |
| lorebook entries | `content` only, when a key appears in recent messages or when `constant` is set | `comment` and `keys` are never sent, so `content` must make sense alone. An entry arrives in the reply after its key comes up, so pick keys the user or the character will actually say. `secondary_keys` narrow when an entry fires. |

## Consequences

- Load-bearing traits belong in `personality` and `description`, which are
  always sent. Repeat the one shared cause in `scenario` and a
  `mes_example` block so it shows from more than one angle.
- Text near the end of the prompt weighs most. That is why `first_mes`,
  recent messages, and `post_history_instructions` steer harder than
  `description`.
- A long permanent card pushes recent chat out of context, so the character
  seems to forget. Cut lines that never change a reply, or move them to a
  lorebook entry.
- Detail that matters only sometimes (a place, a person, an old event) goes
  in a lorebook entry keyed to the words that bring it up.
