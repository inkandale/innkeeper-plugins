# sillytavern

Pushes cards to a [SillyTavern](https://github.com/SillyTavern/SillyTavern)
server as characters, and lorebooks as world info files, and reads both back
so Innkeeper can show changes made in SillyTavern.

```bash
innkeeper call POST /instances '{"plugin": "sillytavern", "name": "SillyTavern",
  "config": {"url": "https://sillytavern.example.com", "auth": "account", "username": "owner"},
  "secrets": {"password": "<password>"}}'
```

## Reaching the server

SillyTavern's `whitelistMode` is on by default and allows only localhost.
Either:

- add Innkeeper's address to `whitelist` in SillyTavern's `config.yaml`, or
- set `whitelistMode: false` and turn on basic auth or accounts.

Behind a proxy that sets `X-Forwarded-For`, that address must be in
`whitelist` too, unless you turn off `enableForwardedWhitelist`.

## Auth modes

Set `auth` to match the server; left out, it is `none`. `username` is
required unless `auth` is `none`.

| `auth` | Server setting |
| --- | --- |
| `none` | No login. |
| `basic` | `basicAuthMode`. With user accounts on, it also needs `perUserBasicAuth`. |
| `account` | `enableUserAccounts`. |

One username and password can't cover shared basic auth plus an account
login, so that setup isn't supported.

SillyTavern allows 5 login attempts per minute per address. If it rejects the
password, the plugin stops logging in until you change the instance's config
or secrets.

## What it syncs

- Card fields SillyTavern keeps.
- The avatar, uploaded only when it changes. SillyTavern needs an image, so
  a card without one gets a plain gray placeholder, which reads back as no
  avatar until you set an image in SillyTavern.
- Lorebooks as world info files, linked by the character's world setting.
  A character has one lorebook; a card with two fails its push.
  A push links the character to its pushed lorebook, or clears the link when
  the card has none, so a world set in SillyTavern is replaced.
  Character filters on world info entries don't sync.
  Entry fields SillyTavern has no place for, such as RisuAI's `mode`, are
  kept in the entry's `extensions.innkeeper` and read back as pushed. A field
  SillyTavern fills in with a default, such as an empty `secondary_keys`,
  reads back as pushed until a save in SillyTavern changes it; its browser
  save adds defaults to every entry.
- Importing from SillyTavern takes each character's world link and its
  image, except the placeholder of a card pushed without one.
- Deletes, when you archive a card or lorebook and choose to delete it from
  linked apps. Deleting a character also deletes its chats in SillyTavern,
  and this can't be undone.

The plugin keeps its own keys in `extensions.innkeeper` on characters, world
files, and entries. Leave them alone. SillyTavern's "Duplicate" copies them, so
the two copies share a key and a later push may update either one. Don't
duplicate a pushed character in SillyTavern; push a new copy from Innkeeper
instead.

Renaming a character in SillyTavern changes its file name, so the next push
makes a new copy.

An open SillyTavern tab keeps its own copy of a character or world file, and
its next save overwrites a push. Reload the tab after pushing. Drift checks
show such overwrites as changes.

Point only one Innkeeper at a SillyTavern user. The plugin finds its earlier
copies by keys that are unique only within one Innkeeper, so a second one
could take over the first's characters and world files.

Two new lorebooks with the same name, pushed at the same moment from
Innkeeper's API and its worker, can land in one world file.
