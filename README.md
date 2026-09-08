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
- `history report --mode MODE [--section KEY] [--account-id ID] [--older-than DURATION] [--output FILE]` — analyze watch history without changing Plex.
`health ping` — bounded identity liveness check.
`health check` — identity plus library-access check with bounded media-byte verification (Range bytes=0-1024, download=1).
`serve --listen ADDR` — local HTTP adapter for Uptime Kuma (`GET /plex/<account>/<server>` → 200/503 JSON with classification; binds to `3003` by convention).
- `api GET /path` — read-only access to any PMS endpoint.

### Watch-history reports

`history report` has four read-only modes. It supports `--section KEY` to restrict
history (and library-item modes) to one exact library section key, and
`--account-id ID` to restrict the fetched history to one Plex account. The report
command does not support `--json`.

```bash
plexctl history report --mode export --output history.csv
plexctl history report --mode export --output history.jsonl
plexctl history report --mode summary
plexctl history report --mode unwatched --section 1
plexctl history report --mode inactive --older-than 2160h
```

- `export` writes normalized view records to `--output`; `--output` is required
  for this mode and is not available to other modes. `.csv` and `.jsonl` outputs
  append to an existing file; `.csv` adds its header only for a new or empty file.
  A `.json` output is intentionally rejected rather than treated as JSON Lines.
- `summary` prints deterministic per-account and per-library-section view totals,
  first/last view times, and total duration.
- `unwatched` lists library items with no matching view-history record. It is
  distinct from `inactive`: an item that was watched long ago is not unwatched.
- `inactive` lists only library items whose most recent recorded view is strictly
  older than `--older-than`; the duration must be positive Go duration syntax
  (for example, `2160h`). Items with no recorded view are not inactive.

View-history exports can contain sensitive local viewing data. Store output files
appropriately. All report modes issue only PMS read requests (GET) and never
mutate Plex; the only write an export performs is appending the requested local
`.csv` or `.jsonl` file.

### Library-maintenance previews

`library maintenance preview` is a read-only, deterministic tabular report of
library-hygiene **candidates**. It never cleans up, refreshes posters, edits
metadata, deletes collections, or otherwise changes Plex. Use one of exactly
four modes:

```bash
plexctl library maintenance preview --mode empty-collections
plexctl library maintenance preview --mode duplicates
plexctl library maintenance preview --mode missing-posters
plexctl library maintenance preview --mode unmatched
plexctl library maintenance preview --mode duplicates --section 1
```

```text
plexctl library maintenance preview \
  --mode empty-collections|duplicates|missing-posters|unmatched \
  [--section SECTION_KEY]
```

`--mode` is required. `--section` optionally limits the scan to one exact
library section key; it does not perform a fuzzy name match. The preview does
not support `--json` and writes no output files or local databases.

- **`empty-collections`** reports a collection only when retrieving its items
  succeeds and returns exactly zero items. A failed collection request is an
  error, not an empty collection.
- **`duplicates`** groups normal media items by normalized title within the same
  section, regardless of year or media type. Normalization trims Unicode
  whitespace, collapses internal whitespace, and compares case-insensitively.
  The original title is retained in output.
- **`missing-posters`** reports normal media items with no `thumb`, or whose
  safe relative poster path fails a bounded authenticated `GET` probe with
  `Range: bytes=0-1023`. A failed probe is reported as `probe_failed`; no full
  poster is downloaded.
- **`unmatched`** reports normal media items with no nonempty Plex GUID values.

All preview requests are read-only PMS `GET`s; there are no mutation requests
and no local persistent writes. Rows are candidates for review, not automatic
cleanup instructions. Output never includes thumbnail URLs or paths, server
base URLs, tokens, headers, response bodies, or poster bytes.

### Read-only audit reports

The following five audit commands are deterministic, tab-separated, **stdout-only**
reports. They inherit `--server` and `--timeout`, reject `--json`, create no CSV,
JSONL, database, cache, snapshot, or other local artifact, and use authenticated
PMS `GET` requests only:

```bash
plexctl library integrity report --mode storage|unavailable|duplicates|suspicious
plexctl sessions diagnostics
plexctl server maintenance status
plexctl playlists audit
plexctl collections audit --section SECTION_KEY
```

`library integrity report` requires exactly one `--mode`:

- **`storage`** prints per-section counts for declared media parts and known
  declared bytes. Missing byte sizes remain unknown rather than being treated as
  zero.
- **`unavailable`** checks declared media parts with a safe PMS-relative,
  authenticated `GET` probe. Each probe requests `Range: bytes=0-1023`, consumes
  at most 1024 bytes even if PMS ignores the range, and never downloads a full
  media file.
- **`duplicates`** identifies repeated safe part paths through a deterministic
  opaque fingerprint; it never prints the underlying path.
- **`suspicious`** identifies malformed media declarations, including missing
  media or part data, unsafe or blank part references, and declared zero-byte
  parts. Intentionally omitted sizes remain `size_unknown` rather than guessed.

`collections audit` requires `--section SECTION_KEY`, which is an exact library
section key, not a fuzzy name. The other four commands accept no audit-specific
positional arguments or flags. `sessions diagnostics` prints active-session
metadata plus stable decision-state totals. `server maintenance status` reports
current activities, Butler task configuration, and the updater status PMS already
reports. `playlists audit` and `collections audit` enumerate each container and
its items, identifying empty containers, duplicate item references, and malformed
or inaccessible references.

Every audit completes validation, required enumeration, and any applicable probe
before emitting its TSV header or row. A malformed response, retrieval error, or
failed probe is an error—not an empty, healthy, or partial result—and produces no
report rows. External text is control-safe TSV encoded. Audit output and failures
do not reveal PMS base URLs, tokens, request headers, response bodies,
authenticated paths, raw media paths, filenames, or part references; duplicate
correlation uses only opaque fingerprints.

These reports are observation only. They do **not** repair unavailable files,
scan or refresh libraries, alter metadata or posters, optimize or clean storage,
terminate sessions, check/download/apply updates, start or stop Butler work, or
create, edit, reorder, delete, or otherwise mutate playlists or collections.
They also do not use filesystem access, SSH, raw API writes, exports, local
persistence, or automatic remediation. A future mutation would require a typed
command, explicit confirmation design, fresh live-contract verification, and
independent review.

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
- `sharing removed purge --older-than DURATION --yes [--dry-run]` — permanently delete locally recorded revocations strictly older than the duration. Use Go duration syntax such as `2160h` (not `d`); `--yes` is required for deletion. Dry-run prints the local match count and does not modify history. Purge never contacts Plex or loads credentials.

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
