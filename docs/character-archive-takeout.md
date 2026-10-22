# Importing the Character Archive takeout

The Character Archive takeout (char-archive.evulid.cc, torrent parts 1 and 2)
holds a SQL dump of chub.ai and several other sites, plus folders of cards
from sites the dump doesn't cover. This guide says which plugin imports each
part and into which library.

## What to import

| Collection | Torrent file | Plugin | Library |
|---|---|---|---|
| chub.ai, Pygmalion Booru, Nyai.me, RisuRealm, webring, catbox, and 69k JanitorAI cards | part 2's `char_archive_database_01-05-2025.sql.7z`, and each part's `hashed-data` file | `chub-archive` | Chub archive |
| chub.ai mega.nz scrape, early 2023 | part 1's `files/historical/chub_ai-mega-nz_scrape.7z` | `card-import` | Chub archive |
| jaicards: about 140k JanitorAI cards, and about 40k more from Hugging Face | part 2's `files/historical/jaicards/cards.7z.001` and the parts after it, and `89ab.zip` below | `card-import` | JanitorAI |
| VenusAI, 05-27-2023, and the VenusAI Discord | part 1's `chub-archive_part-1_third-party.7z` | `card-import` | VenusAI |
| Pygmalion Discord Server, 04-18-2023 | the same file | `card-import` 0.3.0 or later | Pygmalion |
| Roko's Basilisk | the same file | `card-import` 0.3.0 or later | Roko's Basilisk |

Skip the rest:

- `chub-archive_part-1_chub-ai`, `_catbox`, `_janitorai`, and `_webring`: their
  cards are in the SQL dump.
- `files/historical`'s Pygmalion, Roko's Basilisk, and VenusAI files: the same
  cards as `third-party`.
- `char_archive.sql.7z`: an older dump; the 01-05-2025 one replaces it.
- The logs, `proxy_stats`, `other`, and jaicards' `html` and `original json`:
  chats, stats, and other copies of jaicards, not cards to import.

The takeout's jaicards lacks the `cards/8`, `9`, `a`, and `b` folders, about
40k cards. They're in the original dataset,
[AUTOMATIC/jaicards](https://huggingface.co/datasets/AUTOMATIC/jaicards), as
`89ab.zip`. It's 22 GB; `-C -` resumes an interrupted download:

```bash
curl -L -C - -o 89ab.zip https://huggingface.co/datasets/AUTOMATIC/jaicards/resolve/main/89ab.zip
shasum -a 256 89ab.zip  # 45b39e77abccc8ef812afbe664eab9d873ffb24ee3f035421cfa62f8218a3cfe
```

## Set up

1. Extract the files above into one folder, keeping each archive's folders.
   `89ab.zip` holds a `cards` folder too: extract it in the same place as
   jaicards' `cards.7z`, so its four folders join the others.
2. Mount that folder read-only where the worker runs imports, such as
   `/archive`; on Kubernetes, the worker container. It only has to stay
   mounted while an import runs.
3. Set the `card-import` instance's **Folder** to `/archive`. Each import's
   `path` below is relative to it.
4. Create the libraries in the table under Settings > Libraries. In each,
   set **Duplicates** to Near and **Keep** to Oldest before you import into it.

Duplicates are found only within one library. When an import that changes
cards finishes in a library with duplicates on, Innkeeper runs the library's
hide job. The job checks every card in the library, not only the new ones,
and archives each extra copy with the reason `duplicate`; it deletes
nothing. Keep Oldest keeps the card imported first, so import a library's
main source first: the SQL dump before the mega.nz scrape.

## Import

Import the SQL dump first, as the
[chub-archive README](../chub-archive/README.md) describes.

Then start each folder import from the card-import instance, into its
library, with these params:

| Collection | Params |
|---|---|
| mega.nz scrape | `{"path": "Chub mega.nz Scrape"}` |
| jaicards | `{"path": "cards"}` |
| VenusAI | `{"path": "chub-archive_part-1/third-party/Venusai 05-27-2023"}`, then `{"path": "chub-archive_part-1/third-party/Venusai Official Discord Server"}` |
| Pygmalion | `{"path": "chub-archive_part-1/third-party/Pygmalion Discord Server 04-18-2023"}` |
| Roko's Basilisk | `{"path": "chub-archive_part-1/third-party/Roko's Basilisk Archive"}` |

Through the API, an import is `POST /imports` with
`{"instance_id": <card-import instance>, "library": <library>, "params": {...}}`.

## What to expect

- jaicards spreads about 140k PNGs over shard folders. On a network share,
  listing them alone takes minutes, and the import takes hours.
- Each card-import card is its own card: a mega.nz card doesn't join its
  chub.ai copy from the dump. The hide job archives the copy instead.
- Roko's Basilisk has about 300 cards as both a PNG and a JSON file. With
  duplicates on, the hide job archives one of each pair.
- A few files aren't cards: Pygmalion has one malformed JSON file, and Roko's
  Basilisk has ` - roko.json`, a list of every card. The import skips them,
  and its progress counts what it skipped.
