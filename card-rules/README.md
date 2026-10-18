# card-rules

Scores cards for obvious structural problems, without a model. It offers the
`enrich` capability and needs no settings. It's meant as a cheap first pass
over a large library, such as an imported archive, before any AI scoring.

## Scores

| Key | Value |
|---|---|
| `quality` | 100, less a weight for each problem; shown on the library grid |
| `issues` | The problems found, so you can see why a card scored low |
| `tokens` | The card's estimated size: its text fields' characters divided by four |

| Issue | When | Weight |
|---|---|---|
| `empty description` | The description is under 150 characters | 40 |
| `thin description` | It's under 300 | 15 |
| `no greeting` | The first message is under 40 characters | 35 |
| `short greeting` | It's under 100 | 10 |
| `template text` | A placeholder an author left unfilled, such as `[CHARACTER NAME]`, `insert name here`, or `TODO` | 25 |
| `very long` | Over 8,000 estimated tokens | 15 |

A card gets at most one of each pair: `empty` or `thin`, `no` or `short`.
`quality` doesn't go below 0. Each value's meta names the rules' version,
`card-rules-1`, which changes whenever a rule or weight does.

## How the rules were chosen

The thresholds were checked against 500 random cards from a Chub archive
library in October 2026. Chub's own rating stood in for "a card worth
keeping": 241 of the 500 were rated 4 or 5. A good rule flags few of those.

| Issue | Cards flagged | Of the 241 rated 4 or 5 |
|---|---|---|
| `empty description` | 15 | 1 |
| `no greeting` | 11 | 2 |
| `short greeting` | 11 | 2 |
| `thin description` | 9 | 2 |
| `template text` | 2 | 0 |
| `very long` | 0 | 0 |

458 cards scored 100 and 22 scored below 70. Looser template patterns, such
as `todo` and `[]`, flagged 26 cards, half of them rated 4 or 5: `todo` is
Spanish for "all", and empty brackets are common in some card formats. The
largest card in the sample was about 6,000 tokens, so `very long` is a
guard for extreme cards rather than a common flag.

The plugin can't tell whether a card has an image: enrich plugins get only
the card's data. Every card in the sample had one.
