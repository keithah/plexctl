# External Sharing Removal History Design

## Scope

`plexctl` will preserve a local, SQLite-backed audit history of successful external Plex share revocations. The history is local to the operator's machine; it is not uploaded to Plex and no existing Plex user or share data is backfilled.

The database keeps records indefinitely by default. Operators can explicitly purge records older than a supplied duration.

## Commands

```text
plexctl sharing removed [--json]
plexctl sharing removed purge --older-than DURATION --yes [--dry-run]
```

- `sharing removed` reads local history only and lists entries newest first. `--json` emits the same records in deterministic order.
- `sharing removed purge` is a local destructive action. It requires a positive duration expressed in Go duration syntax (for example `2160h` for 90 days) and `--yes`.
- `--dry-run` validates the duration, opens the local database read-only, and prints the number of matching local records without deleting them. It makes no Plex requests and never mutates the database.
- A cutoff is calculated as `now - duration`; only records with `removed_at < cutoff` are eligible. Boundary records at the cutoff remain.
- Purge never contacts Plex and never changes Plex sharing state.

## Storage

Create `internal/sharinghistory`, backed by the pure-Go `modernc.org/sqlite` driver. The default path is:

```text
$XDG_DATA_HOME/plexctl/sharing-history.db
```

falling back to `os.UserConfigDir()/plexctl/sharing-history.db` when a data directory is unavailable. `PLEXCTL_SHARING_HISTORY_DB` provides an explicit test/operator override.

The directory is created with mode `0700`; the SQLite database is created with mode `0600`. Database handles are opened only for live successful revocation recording, listing, and non-dry-run purge.

Schema version 1 consists of one append-only table:

```sql
CREATE TABLE removed_external_shares (
  id INTEGER PRIMARY KEY,
  removed_at TEXT NOT NULL,
  plex_user_id INTEGER NOT NULL,
  username TEXT NOT NULL,
  email TEXT,
  share_id INTEGER NOT NULL,
  server_name TEXT NOT NULL,
  server_client_identifier TEXT NOT NULL,
  all_libraries INTEGER NOT NULL,
  pending INTEGER NOT NULL,
  library_section_ids TEXT NOT NULL
);
CREATE INDEX removed_external_shares_removed_at_idx
  ON removed_external_shares (removed_at DESC, id DESC);
```

`library_section_ids` is canonical JSON containing the global Plex.tv section IDs held by the share immediately before removal. All persisted timestamps are UTC RFC3339Nano.

## Revocation flow

The existing revocation safety flow remains authoritative:

1. Validate positive share ID, `--server`, and `--yes`.
2. Resolve a fresh owned Plex resource.
3. Fetch shared external users and prove the target is exactly one non-Home, owned share for that resource.
4. Fetch the target's current library grants.
5. Send the existing exact, bodyless DELETE.
6. Only after Plex returns a successful DELETE, write one local snapshot record and print `REVOKED`.

No history record is created for validation errors, dry runs, network failures, non-success DELETE responses, or local history write failure before the DELETE. If local persistence fails after a successful DELETE, the command returns a distinct error explaining that Plex revocation succeeded but its local history could not be recorded. It must never claim an unperformed Plex operation or attempt a second DELETE.

The history snapshot contains the matched external user's identity, the validated share's original metadata, the fresh server identity, and library grants fetched before DELETE. Credentials, tokens, raw API error bodies, and connection URLs are never stored or printed.

## Error handling

- SQLite initialization/schema errors fail the specific local-history command without network activity.
- Database writes use parameterized statements and a transaction where needed; no SQL is constructed from user or Plex data.
- Listing an absent database returns an empty result and does not create a database file.
- Purge detects malformed, zero, and negative durations before database access.
- All output remains token-safe.

## Tests and verification

Use strict TDD. Tests cover:

- schema initialization, private storage location/permissions, record persistence, JSON grant encoding, and deterministic newest-first ordering;
- empty-history behavior without DB creation;
- `sharing remove` snapshots exactly one record only after a validated successful DELETE, including matched user/server/grants;
- no record for validation rejection, Home/foreign/ambiguous shares, Plex DELETE failure, and `--dry-run`;
- `sharing removed` table and JSON output without any Plex request;
- purge validation, boundary cutoff behavior, confirmation requirement, dry-run zero writes, and actual deletion count;
- full suite, race checks for the touched packages, `go vet`, `go build`, `gofmt`, secret scan, API-coverage check, and a local-only SQLite command acceptance run.

## Non-goals

- Backfilling shares revoked before this feature exists.
- Restoring/reinviting users from history.
- Persisting credentials, Plex tokens, full raw Plex responses, or arbitrary Plex settings.
- Automatically scheduled retention or automatic deletion.
- Any mutation to Plex from listing or purging local history.
