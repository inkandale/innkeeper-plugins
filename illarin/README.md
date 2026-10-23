# illarin

Imports cards and lorebooks from [Illarin](https://illarin.com) through its
connected-app API, with the `work:receive` permission. Illarin's robots.txt
disallows `/api/` and `/download/`, so this plugin doesn't scrape pages.

| Import params | What it imports |
|---|---|
| `{"url": "https://illarin.com/a/{id}/{name}"}` | That work's latest version |
| `{"sent": true}` | Every work you sent to Innkeeper from its Illarin page |

## Connecting

The first import has no credentials, so it starts Illarin's device flow:

1. The job's page shows an Illarin address and a code, such as `ABCD-EFGH`.
2. Open the address, check that it shows the same code, and turn on
   receiving works.
3. The import continues once you approve. The code expires after ten minutes.

To disconnect, revoke Innkeeper under connected apps in Illarin's account
settings. The next import shows a new code. Changing `base_url` also starts a
new connection.

The credentials live in the plugin's database schema, not in instance
secrets: Illarin rotates the refresh token on every refresh, and the plugin
has to save each new one.

## Behavior

- An import waits on Illarin's send queue, 25 to 30 seconds per wait, and
  ends when the queue is empty. A URL import takes at least one wait to
  acknowledge what it imported.
- A send that can't be read is logged as a warning and acknowledged, so
  Illarin stops offering it.
- Each item is keyed by `{base_url}/a/{id}`, so importing a work again adds
  nothing unless it changed.
- Illarin sends `chara_card_v3`, `chara_card_v2`, or `charx` for a card and
  `lorebook_sillytavern` for a lorebook.

## Testing against a real Illarin

Set `base_url` to your fork's https address, such as
`https://illarin.example.test`, and run an import.
