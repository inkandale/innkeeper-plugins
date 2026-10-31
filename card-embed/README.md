# Similar cards

Gives each card a vector, so the card page shows similar cards and search
can rank by meaning.

## Set up

1. In the AI plugin, add a connection that provides embeddings.
2. Optional: add a chat connection for summaries. Similar cards then match
   on what a card is about rather than how it's written. A small model is
   enough, such as Gemma 4 E4B.
3. Set up Similar cards with those connections. The embedding connection is
   required.
4. Run it from the library's selection bar on the cards you have. New and
   edited cards are done as they change.

PostgreSQL needs pgvector for core to store vectors. Without it, the plugin
writes only premises and themes, so it needs a summary connection; with
neither, a run fails and says so.

With one plugin providing vectors, core uses it for search and similar cards
and you set nothing. With several, pick one in core's settings.

## Cost

Each change makes one embedding call, plus one chat call when a summary
connection is set. The summary calls run up to "Cards summarized at once" at
a time (default 4). Raise it for a hosted or batching model server; lower it
for one that runs a request at a time.

## Values

| Key | What |
|---|---|
| `premise` | Two or three sentences on the card's setup, with no proper names |
| `themes` | Five to ten lowercase tags |
| `embedding` | The card's vector, kept by core |

A vector belongs to a space: the embedding model, plus the summary
connection and prompt when one is set. Changing any of them starts a new
space, so run the plugin on your cards again. Until then, those cards fall
back to similar-by-tags.

## Failures

A card whose summary fails gets no vector, so no card mixes vectors from
summaries and from raw text. The error shows on that card; run it again.
