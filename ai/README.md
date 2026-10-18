# ai

Connects a language model for other plugins to use, through
[any-llm-go](https://github.com/mozilla-ai/any-llm-go). It offers the `ai`
and `settings` capabilities, so it needs a core that has them.

Each instance is one connection. `provider` and `model` are required.
`api_key` is optional for `openai-compatible`, `ollama`, `llamacpp`, and
`llamafile`; the other providers need one. `openai-compatible` also needs
`base_url`, such as `http://localhost:1234/v1` for LM Studio or
`https://openrouter.ai/api/v1`.

## What a connection provides

Tick what the connection is for under **Provides**; chat is the default.

| Kind | Service | Providers |
|---|---|---|
| `chat` | `ai.chat` and `ai.decide` | All |
| `embed` | `ai.embed` | openai, openai-compatible, gemini, mistral, ollama, llamacpp, llamafile |
| `decide` | `ai.decide` | Any server with `/v1/systemone` under `base_url` |

A consumer, such as forge-ai, picks an instance that offers the service it
needs. `ai.chat` returns the model's text as it is, including any
`<think>` block, and returns tool calls for the consumer to answer.

## Decisions

`ai.decide` answers typed questions about a state with probabilities: see
the core's protocol docs. There are two ways to answer it:

- **`decide`** sends the questions to a decision model's `/v1/systemone`,
  such as [vllm-sr/d3](https://huggingface.co/vllm-sr/d3) on llama.cpp or
  vLLM, or TypeSafe's hosted API at `https://api.typesafe.ai/v1`. Set
  `base_url` to the URL `/systemone` is under, and `model` to the name the
  server knows it by. `api_key`, if set, goes as a bearer token.
- **`chat`** asks the chat model for each answer's probabilities. Its score
  and confidence are computed the same way, then the confidence is halved,
  since a chat model's stated probabilities are rougher. This is the
  fallback when no decision model is connected.

A connection that ticks both answers through `/v1/systemone`.

llama.cpp's `/v1/systemone` reads the whole state again for every question,
so its time grows with the number of questions. A choice question's
`criteria` must be an object of option name to description there.

## Settings page

- **Load choices** on Model lists the provider's models. Anthropic doesn't
  list them, so type the name.
- **Test connection** makes one small real call per ticked kind, a
  one-token chat, one embedded word, or one yes-or-no decision, and
  reports each with its latency.
  It gives up after 30 seconds for all the calls together.
