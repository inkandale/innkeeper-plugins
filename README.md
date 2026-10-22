# innkeeper-plugins

Plugins for [Innkeeper](https://github.com/inkandale/innkeeper): importers,
catalogs, app targets, codecs, AI actions, enrichers, and login providers.

Each plugin is a folder with a `plugin.toml` manifest. `index.toml` lists every
plugin. The protocol and contract test kit live in the `innkeeper` repo.

To import the Character Archive takeout, see
[docs/character-archive-takeout.md](docs/character-archive-takeout.md).

## Building and checking

```bash
make build                    # each plugin's binary, into its folder
make check                    # the core's contract kit, against each plugin
go run ./internal/indexcheck  # index.toml matches the plugin folders
```

The core version is pinned in `go.mod` as a tool. Move it with
`go get -tool github.com/inkandale/innkeeper/cmd/innkeeper@<commit>`;
that sets both the `protocol` package and the contract kit.

## Writing a plugin

The protocol is in the core's
[docs/protocol.md](https://github.com/inkandale/innkeeper/blob/main/docs/protocol.md).
Start from the plugin closest to yours:

| Capability | Copy |
|---|---|
| `import` from files | `card-import` |
| `import` from a database dump, with checkpoints | `chub-archive` |
| `push` and `pull` to an app | `lumiverse` or `sillytavern` |
| `sites` and `pull` from public pages | `url-import` |
| `codec` | `card-codec` |
| `auth` | `password-login` |
| `enrich` without a model | `card-lint` |
| `action`, `records`, `settings`, or `enrich` with a model | `forge-ai` |
| `ai` | `ai` |

### Files to add or change

1. A folder whose `plugin.toml` has `command = ["./<folder>"]`.
2. An `index.toml` entry with `path = "<folder>"` and the same name,
   version, protocol, capabilities, description, and category as
   `plugin.toml`. `go run ./internal/indexcheck` checks they match.
3. The built binary, `/<folder>/<folder>`, in `.gitignore`.
4. An icon, if you want one: a file in the folder named by `icon` in
   `plugin.toml`. `make install` copies `icon.*`.
5. A `check-config.json` with the instance config for `make check`, if the
   contract kit needs one. `make check` sends it only when
   `INNKEEPER_PLUGIN_DATABASE_URL` is set.

The version lives in `plugin.toml` and `index.toml` only; `protocol.Serve`
reads it from `plugin.toml`.

### Shared code

| Package | Use it for |
|---|---|
| `protocol.Serve` (core) | The main loop: `initialize` from `plugin.toml`, per-instance state, method dispatch. Build errors with `protocol.Fail` and `protocol.InvalidParams` |
| `card` | Reading and writing card files: CCv3, V2, and V1 as JSON and PNG, CharX, and reading Pygmalion and Roko's Basilisk JSON |
| `internal/pluginio` | Reading input files with a size cap, and writing to `blob_tmp` |
| `internal/httpkit` | Calling an app's JSON API: a reply size cap, redirect refusal, and HTTP status to protocol error, with 5xx and 429 retryable |
| `internal/pgtest` | A PostgreSQL schema per test, from `INNKEEPER_TEST_DATABASE_URL` |

### Tests

An app target has two test suites:

- `fake_test.go`: an in-memory `httptest` server with the app's routes,
  written from the app's source. The unit tests run against it.
- `live_test.go`: `TestLive`, against a real server. It skips unless its
  environment variables are set, such as `LUMIVERSE_LIVE=1` and
  `LUMIVERSE_URL`; the doc comment on `TestLive` lists them.

## Publishing

Every push to `main` releases each plugin version that has no release yet,
tagged `<name>-v<version>`, with a tarball for linux/amd64 and linux/arm64.
Bump `version` in both `plugin.toml` and `index.toml` to publish a change; a
release is never rebuilt. A change to `categories`, or to a plugin's
`category` or `recommended`, needs no bump: the published index takes them
from `index.toml`.

The `index` release holds `index.toml`, which lists every released version.
Innkeeper's default plugin source points at it:

```text
https://github.com/inkandale/innkeeper-plugins/releases/download/index/index.toml
```

To build a release locally, run
`go run ./internal/publish -out dist -url <release download URL> <folder>`.
