# plexctl

`plexctl` is an unofficial Go CLI and reusable client for Plex Media Server. It
provides safe, scriptable server inspection and health checks while preserving a
raw read-only API escape hatch for the broad PMS API surface.

The current release is a CLI/client, not an MCP server or HTTP service. Its
health package is intentionally separated from the CLI so a small, local HTTP
adapter can be added for external monitors such as Uptime Kuma without
duplicating Plex authentication, resource discovery, connection selection, or
health classification.

## Install

```bash
go install github.com/keithah/plexctl/cmd/plexctl@latest
```

## Configure

```bash
plexctl auth login
# Open the printed plex.tv/link URL and authorize the account.
plexctl accounts list
plexctl servers list
plexctl servers use SERVER_ID
plexctl server identity --json
```

`auth login` can be run repeatedly for multiple Plex accounts. Each account's
token is stored in the operating system credential store; tokens are never
stored in the config file or printed by the CLI. The config file contains
account metadata and discovered server connections and is written with mode
0600. The legacy environment-token configuration remains available for
automation.

When discovery advertises a remote HTTP URL, `plexctl` upgrades it to HTTPS.
For IP-literal endpoints only, certificate verification is disabled because
Plex's certificate cannot match the IP address; hostname-based endpoints keep
normal certificate verification.

## Commands

- `server info` — show server configuration and capabilities.
- `auth login [--name NAME]` / `auth logout ACCOUNT` — add or remove a Plex account.
- `accounts list` / `accounts use ACCOUNT` — list and select accounts.
- `servers list` / `servers use SERVER_ID` — list and select discovered servers.
- `server identity` — inspect the configured PMS identity.
- `library list` — inspect libraries.
- `library search TERM [--section KEY] [--limit N]` — search via `/hubs/search`.
- `library recently-added SECTION_KEY [--limit N]` — newest items in a library.
- `library items SECTION_KEY [--sort EXPR] [--limit N]` — browse a library page.
- `metadata get RATING_KEY` / `metadata children RATING_KEY` — retrieve metadata.
- `playlists list` / `playlists get PLAYLIST_ID` / `playlists items PLAYLIST_ID` — inspect playlists and their items.
- `collections list SECTION_ID` / `collections items COLLECTION_ID` — inspect library collections.
- `download-queues get QUEUE_ID` / `items QUEUE_ID` / `item QUEUE_ID ITEM_ID` / `decision QUEUE_ID ITEM_ID` — inspect download queues without mutating them.
- `transcode decision TYPE SESSION_ID [--param key=value]` — inspect universal transcode decisions.
- `transcode subtitles TYPE SESSION_ID [--param key=value]` — query universal subtitle handling.
- `sessions list` — list active sessions.
- `sessions history [--account-id ID] [--section-id ID] [--sort EXPR] [--viewed-at TIME] [--metadata-id ID]` — playback history.
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
