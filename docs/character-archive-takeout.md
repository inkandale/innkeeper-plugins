# Importing the Character Archive takeout

The Character Archive takeout (char-archive.evulid.cc, torrent parts 1 and 2)
holds a SQL dump of chub.ai and several other sites, plus folders of cards
from sites the dump doesn't cover. Its JanitorAI card set, jaicards, is
incomplete; get that one from Hugging Face instead. This guide says which
plugin imports each part and into which library.

## What to import

| Collection | Source | Plugin | Library |
|---|---|---|---|
| chub.ai, Pygmalion Booru, Nyai.me, RisuRealm, webring, catbox, and 69k JanitorAI cards | part 2's `char_archive_database_01-05-2025.sql.7z`, and each part's `hashed-data` file | `chub-archive` | Chub archive |
| chub.ai mega.nz scrape, early 2023 | part 1's `files/historical/chub_ai-mega-nz_scrape.7z` | `card-import` | Chub archive |
| jaicards: about 180k JanitorAI cards | the four zips from Hugging Face, below | `card-import` | JanitorAI |
| VenusAI, 05-27-2023, and the VenusAI Discord | part 1's `chub-archive_part-1_third-party.7z` | `card-import` | VenusAI |
| Pygmalion Discord Server, 04-18-2023 | the same file | `card-import` 0.3.0 or later | Pygmalion |
| Roko's Basilisk | the same file | `card-import` 0.3.0 or later | Roko's Basilisk |

Skip the rest:

- `chub-archive_part-1_chub-ai`, `_catbox`, `_janitorai`, and `_webring`: their
  cards are in the SQL dump.
- `files/historical`'s Pygmalion, Roko's Basilisk, and VenusAI files: the same
  cards as `third-party`.
- `char_archive.sql.7z`: an older dump; the 01-05-2025 one replaces it.
- Part 2's `files/historical/jaicards`: it lacks the `cards/8`, `9`, `a`, and
  `b` folders, about 40k cards. Hugging Face has the whole set.
- The logs, `proxy_stats`, and `other`: chats, stats, and articles, not cards.

jaicards is the dataset
[AUTOMATIC/jaicards](https://huggingface.co/datasets/AUTOMATIC/jaicards): four
zips of about 22 GB each, needing no login. `-C -` resumes an interrupted
download:

```bash
for z in 0123 4567 89ab cdef; do
  curl -L -C - -o $z.zip https://huggingface.co/datasets/AUTOMATIC/jaicards/resolve/main/$z.zip
done
shasum -a 256 *.zip
```

| File | SHA-256 |
|---|---|
| `0123.zip` | `db9e83c74316bf6a9686c60524c4992d16cf4e058548fae285ac2188df1a00b3` |
| `4567.zip` | `6e3f4f1da37febd3f88b37480928d90f11d8822c57b24b449a5b51963c8c5593` |
| `89ab.zip` | `45b39e77abccc8ef812afbe664eab9d873ffb24ee3f035421cfa62f8218a3cfe` |
| `cdef.zip` | `1f24adda26067e1c0546e4b3c2755d5e227c4a325aebc3f464804c69eb1e4b82` |

## Set up

1. Extract the files above into one folder, keeping each archive's folders.
   Each jaicards zip holds a `cards` folder; extract all four in the same
   place, so their folders `0` to `f` share one `cards`. They are ZIP64,
   which macOS `unzip` can't read; use `python3 -m zipfile -e 0123.zip .`.
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

- jaicards spreads about 180k PNGs over 16 shard folders. On a network share,
  listing them alone takes minutes, and the import takes hours.
- Each card-import card is its own card: a mega.nz card doesn't join its
  chub.ai copy from the dump. The hide job archives the copy instead.
- Roko's Basilisk has about 300 cards as both a PNG and a JSON file. With
  duplicates on, the hide job archives one of each pair.
- A few files aren't cards: Pygmalion has one malformed JSON file, and Roko's
  Basilisk has ` - roko.json`, a list of every card. The import skips them,
  and its progress counts what it skipped.
