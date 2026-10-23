# crucible

Scores cards with a crucible service. For each card, the plugin sends the
card's data to the service's `POST /score` and stores what it answers with.
The values come from whatever service the URL names.

The one setting is the service URL, such as `http://crucible.example:8080`.
Until you set it, every run fails with "set the service URL".

## What it stores

| Key | Type |
|---|---|
| `quality`, `consistency`, `voice`, `first_impression`, `roleplayability`, `token_efficiency`, `field_governance` | score, 0 to 100 |
| `rating`, `card_type`, `pov`, `format` | text |
| `flags`, `contradictions`, `suggestions` | list |

## The `POST /score` contract

Request body:

```json
{"card_id": 1, "revision_id": 10, "data": {"name": "Mira"}}
```

`data` is the card's CCv3 data without `character_book`.

Answer `200` with the values and optional metadata:

```json
{"values": {"quality": 72.5, "flags": ["minor risk"]}, "meta": {"source": "crucible"}}
```

To fail one card, answer with a non-200 status and `{"error": "reason"}`. The
reason shows as that card's error and the rest of the batch goes on.
