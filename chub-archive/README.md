# chub-archive

Imports the chub.ai archive takeout's SQL dump
(`char_archive_database_01-05-2025.sql`) into an Innkeeper library: one card
or lorebook per archive item, with each of its versions as a revision.

## Setup

1. Extract the takeout's `.7z` files. The plugin reads the extracted dump
   and image folders.
2. Mount the extracted archive read-only where the worker runs imports, the
   worker container on Kubernetes. It only needs to be mounted while an
   import runs.
3. In Innkeeper, create a library for the archive, such as "Chub archive".
4. Set up chub-archive. Set **Image folders** to the `hashed-data` folders
   under the archive's `sql` folder, from every takeout you import.
5. Start an import into that library with chub-archive and these params:

   ```json
   {"dump": "/archive/sql/database_01-05-2025/char_archive_database_01-05-2025.sql"}
   ```

Upgrading from 0.2 drops its catalog tables when the plugin starts. Its image
store isn't touched: delete that folder by hand once the import has run.

## Behavior

- Cards come from the chub, Pygmalion Booru, Nyai.me, RisuRealm, webring,
  and generic (JanitorAI and catbox) tables, and lorebooks from chub's.
- An item's ID is `<table>:<id>`, so importing a newer dump updates the
  same cards. The webring and generic tables have no ID, so theirs is the
  row's card hash and each row is its own item.
- Each row is one version, sent in dump order with the message
  `Version <increment>` and the row's `added` date, or its `created` date
  without one. A row with neither is skipped, since the core orders an
  item's versions by date. Booru items have one version.
- Cards carry these values, shown as enrichments from the instance:

  | Key | From |
  |---|---|
  | `rating` | chub's per-item row, 0 to 5 |
  | `topics` | chub's per-item row |
  | `site` | the table, such as `chub` or `booru` |
  | `hidden` | `yes` when the row or chub's per-item row is hidden |

- An import reads the whole dump, about 12 GB, and resumes from its last
  checkpoint if it stops. A checkpoint from a dump of another size is
  refused. Versions the library already has add nothing.
- Rows that won't decode are skipped and logged with their table, ID, and
  increment.
- A version whose image is missing or unreadable (permission denied, a
  folder, over 32 MiB, or not a PNG, WebP, JPEG, or GIF) is sent without an
  avatar. Other image read or write errors stop the import, and the core
  retries it from its last checkpoint.
