# card-import

Imports CCv3, V2, and V1 cards as PNG or JSON, Pygmalion and Roko's Basilisk
JSON cards, CharX cards, and lorebooks as CCv3
`character_book` JSON or SillyTavern world info.

Set `config.folder` to the folder imports may read. Import params are one of:

| Params | Imports |
|---|---|
| `{}` | Every `.png`, `.json`, and `.charx` file under `config.folder` |
| `{"path": "fantasy"}` | Every such file under `config.folder/fantasy` |
| `{"blob": "<sha256>", "name": "brakka.png"}` | One file uploaded with `POST /blobs`, within 24 hours. `name` is optional and names a lorebook that has none |

A folder import skips files that aren't cards or lorebooks, and symlinks.
The job's progress says how many it skipped; the plugin log says which.
