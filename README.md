# plexctl

`plexctl` is an unofficial Go CLI and reusable client for Plex Media Server. It
provides safe, scriptable server inspection and health checks while preserving a
raw read-only API escape hatch for the broad PMS API surface.

## Install

```bash
go install github.com/keithah/plexctl/cmd/plexctl@latest
```

## Configure

```bash
export PLEX_HOME_TOKEN='your-token'
plexctl config init
plexctl config set home https://plex.example.com:32400 PLEX_HOME_TOKEN
plexctl config use home
plexctl server identity --json
plexctl health check
```

Tokens are read from the named environment variable at runtime and are never
stored in the config file or printed by the CLI. Use a placeholder token only
in documentation. The config file is written with mode 0600.

## Commands

- `server info` — show server configuration and capabilities.
- `server identity` — inspect the configured PMS identity.
- `library list` / `library items SECTION_KEY` — inspect libraries and media.
- `library search TERM [--section KEY] [--limit N]` — search via `/hubs/search`.
- `library recently-added SECTION_KEY [--limit N]` — newest items in a library.
- `library items SECTION_KEY [--sort EXPR] [--limit N]` — browse a library page.
- `metadata get RATING_KEY` / `metadata children RATING_KEY` — retrieve metadata.
- `playlists list` / `playlists get PLAYLIST_ID` / `playlists items PLAYLIST_ID` — inspect playlists and their items.
- `sessions list` — list active sessions.
- `sessions history [--account-id ID] [--section-id ID] [--sort EXPR]` — playback history.
- `health ping` — bounded identity liveness check.
- `health check` — identity plus library-access check.
- `api GET /path` — read-only access to any PMS endpoint.

Read commands accept `--json`, `--server`, and `--timeout`, and every request
honors the configured timeout. Raw API mutations are intentionally rejected
until a typed command with an explicit confirmation gate exists.

`metadata children` targets a route Plex Media Server serves but that is absent
from the pinned contract, so some servers and item types answer 404.

## API contract

The pinned official PMS OpenAPI contract is in `api/plex-pms.openapi.json`. It is
version 1.2.2, contains 205 paths and 258 operations, and is sourced from
<https://developer.plex.tv/pms/>. See `api/README.md` for the normalized JSON
checksum and refresh notes.

## Development

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
go build ./cmd/plexctl
python3 scripts/check-secrets.py
python3 scripts/check-api-coverage.py
```

This project is unofficial and is not affiliated with Plex, Inc.
