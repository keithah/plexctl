# plexctl

`plexctl` is an unofficial Go CLI and reusable client for Plex Media Server. It
provides safe, scriptable server inspection and health checks while preserving a
raw read-only API escape hatch for the broad PMS API surface.

`plexctl` ships a Go CLI, reusable client, and a local HTTP monitoring adapter
(`plexctl serve`). It is not an MCP server. Its health package is separated
from the CLI so the adapter reuses Plex authentication, resource discovery,
connection selection, and health classification without duplication — see
`docs/monitoring.md` for the Uptime Kuma integration.

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
`health ping` — bounded identity liveness check.
`health check` — identity plus library-access check with bounded media-byte verification (Range bytes=0-1024, download=1).
`serve --listen ADDR` — local HTTP adapter for Uptime Kuma (`GET /plex/<account>/<server>` → 200/503 JSON with classification; binds to `3003` by convention).
- `api GET /path` — read-only access to any PMS endpoint.

### External Plex sharing

The `sharing` group manages **external** Plex server shares only; Plex Home and
managed users are excluded.

- `sharing users [--json]` — list external users and their owned-server shares,
  including returned email, pending state, share ID, and grants.
- `sharing libraries --server SERVER_ID [--json]` — list the current global
  Plex.tv library-section IDs eligible for one owned server.
- `sharing invite EMAIL_OR_USERNAME --server SERVER_ID (--libraries ID,ID | --all-libraries) [--dry-run]` — create one external share. The command validates the owned server and current global library IDs before POSTing.
- `sharing update SHARE_ID --server SERVER_ID (--libraries ID,ID | --all-libraries) [--dry-run]` — **replaces** the share's complete library-grant set; it never merges the existing grants.
- `sharing remove SHARE_ID --server SERVER_ID --yes [--dry-run]` — revoke exactly one external server share. `--yes` is mandatory; dry-run never contacts Plex.
- `sharing removed [--json]` — list locally recorded successful share revocations, newest first. It never contacts Plex or loads credentials; only revocations completed successfully after this release appear.

Sharing mutations use the stored protected account credential and fresh Plex.tv
resource discovery. They do not accept token flags. `--dry-run` is a local
preview; it does not resolve remote state, invite, update, or revoke.

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
