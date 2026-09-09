# Read-only Plex audit suite design

## Goal

Add four deterministic, stdout-only Plex audit reports in one PR: media-part integrity, active-session diagnostics, server-maintenance status, and playlist/collection audits. Every command is observation only: it makes authenticated PMS `GET` requests and creates no persistent local output.

## Command surface

```text
plexctl library integrity report --mode storage|unavailable-media|duplicate-parts|suspicious-parts [--section SECTION_KEY]
plexctl sessions diagnostics
plexctl server maintenance status
plexctl playlists audit
plexctl collections audit --section SECTION_KEY
```

All commands inherit the existing `--server` and `--timeout` flags. They reject `--json`; their tab-separated output is deliberately schema-stable and routes every external text field through the existing control-safe TSV encoder. They do not write CSV, JSONL, SQLite, caches, snapshots, or other local artifacts.

`library integrity report` requires exactly one mode. `--section` optionally scopes it to one exact section key using the existing exact section lookup. `collections audit` requires one exact section key because collection enumeration is section-scoped. `playlists audit`, `sessions diagnostics`, and `server maintenance status` have no positional arguments or feature flags.

## Shared read-only and retrieval contract

The new PMS client paths use only documented GET operations. No command sends POST, PUT, PATCH, or DELETE. In particular, maintenance status uses `GET /activities`, `GET /butler`, and `GET /updater/status`; it never calls the mutating `PUT /updater/check`, the Butler start/stop routes, or activity cancellation.

Every report completes prerequisite enumeration and integrity validation before it writes its header or a candidate row. Section media, playlists, collections, and each container's contents use strict offset pagination: each page must provide explicit `size`, `offset`, and `totalSize`; offsets must be contiguous; the first total becomes a snapshot; any later inequality, impossible size, malformed identifier, or lack of progress is an error. A retrieval or probe failure is not converted into an empty/healthy candidate result.

All untrusted output is TSV-escaped. Reports do not expose PMS base URLs, tokens, request headers, response bodies, authenticated paths, raw media file paths, or part keys. When duplicate path correlation is needed, the output uses a deterministic SHA-256-derived opaque identifier; the underlying path is never printed.

## Media-part integrity

The report enumerates eligible normal media items from the selected library sections, then evaluates their declared `Media` and `Part` metadata.

- `storage` emits deterministic per-section counts of eligible items, declared media records, declared parts, and known declared byte totals. It reports an item/part as `size_unknown` rather than manufacturing a total where Plex did not supply a byte size.
- `unavailable-media` reports each declared part whose bounded authenticated part GET fails or produces no media bytes. Probes use only safe PMS-relative media-part routes, request `Range: bytes=0-1023`, consume at most 1024 bytes, and never print the route or filename.
- `duplicate-parts` groups safe, nonempty declared part paths by a normalized opaque path fingerprint within one server. A duplicate candidate includes section/item identity and fingerprint, never the source path.
- `suspicious-parts` reports malformed media metadata: missing media array, media record with no parts, blank or unsafe part reference, and declared zero-byte part. Fields that Plex omits intentionally are represented by a specific status such as `size_unknown`, not guessed as zero.

A malformed item or unsafe part reference stops the whole mode before output. The implementation must not fall back to filesystem, SSH, host-path, or raw `api GET` access.

## Active-session diagnostics

`plexctl sessions diagnostics` expands the existing session model to capture safe diagnostic data Plex supplies: media identity, hierarchy title, session ID, user/client identity and product/platform, playback progress, duration, and video/audio/subtitle decision fields. It emits one deterministic row per active session followed by a stable aggregate grouped by direct play, direct stream, transcode, and unknown decision states.

If required session identifiers or decision containers are malformed, the command fails before printing output. It never calls the session-termination endpoint and never requests media bytes.

## Server-maintenance status

`plexctl server maintenance status` produces a read-only snapshot with three deterministic report blocks:

1. currently reported activities and their progress/state;
2. Butler tasks and their schedule/enabled state;
3. updater status as last reported by PMS.

The command does not trigger refreshes, scans, optimization, update checks, downloads, updates, or Butler work. Missing required response containers and malformed activity/task IDs fail closed without partial output.

## Playlist and collection audits

`plexctl playlists audit` strictly enumerates all playlists and each playlist's items. `plexctl collections audit --section KEY` strictly enumerates collections in the exact selected section and each collection's items. Each report detects:

- empty containers only after a complete successful item listing;
- duplicate item references using an opaque identity derived from a valid rating key;
- malformed or inaccessible item references, including blank identifiers or incomplete item metadata.

No playlist, collection, or item is created, removed, reordered, refreshed, or edited. A failure to enumerate an item listing fails the report and writes no candidate rows.

## Implementation boundaries

Extend `internal/pms/models.go` only with typed, documented response shapes needed for media parts, session decisions/clients/users, activities, Butler tasks, and updater status. Add strict client methods in `internal/pms/client.go`, preserving the existing response-size limits, error redaction, path validation, and paging invariants.

Put pure classification, normalization, grouping, sorting, and aggregate logic into focused internal packages rather than Cobra handlers. CLI commands should validate flags before configuration/network access, invoke typed client methods, and format prevalidated report records through one shared report printer.

## Errors, determinism, and privacy

Validation failures happen before the first request. Retrieval failures identify the report phase and public-safe item identifiers but never leak route paths, URLs, headers, token material, raw paths, filenames, or response content. Output ordering is stable by section key, container rating key, item rating key, then opaque fingerprint/status. Commands output nothing to stdout after any prerequisite failure.

## Tests and acceptance

Tests include pure table-driven detector coverage and built-CLI fixtures for every mode. Fixture assertions must prove only GET requests occurred; no local report files were created; output contains no token, server URL, raw path, or response-body sentinel; and all dynamic TSV fields remain one physical row with stable column count even with C0, DEL, C1, tabs, newlines, and carriage returns.

Pagination regressions cover missing `offset`/`totalSize`, noncontiguous pages, zero progress, and both decreasing and increasing `totalSize` snapshots for section media, playlists, collections, playlist items, and collection items. Probe tests verify a maximum 1024-byte client consumption even when a server ignores Range. Adversarial fixtures cover blank IDs, unsafe part references, zero/missing sizes, malformed decision/activity/task containers, inaccessible items, duplicate fingerprints, and no-output-on-failure behavior.

The PR runs `gofmt -l cmd internal`, `go test ./... -count=1`, `go test -race ./... -count=1`, `go vet ./...`, `go build ./cmd/plexctl`, `python3 scripts/check-secrets.py`, `python3 scripts/check-api-coverage.py`, and `git diff --check origin/main...HEAD`. README documentation describes all reports, their stdout-only/read-only contract, probe load, data-sensitivity limits, and explicit non-goals.

## Non-goals

This PR does not add exports, local persistence, filesystem access, SSH, server scans, library refreshes, metadata changes, poster changes, optimizations, storage cleanup, unavailable-file repair, session termination, update checks/downloads/apply, Butler execution, playlist edits, playlist creation/deletion, collection edits/creation/deletion, raw API writes, or automatic remediation. Any mutation requires a later typed command, explicit confirmation design, fresh live-contract verification, and independent review.
