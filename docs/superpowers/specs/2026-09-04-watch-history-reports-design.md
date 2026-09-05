# Watch-history reports design

## Purpose

Add a read-only reporting surface for Plex playback history. The first delivery produces reproducible exports and analysis without changing Plex state or storing a local history database.

This is the first of five separate, read-first features:

1. Watch-history reports (this design).
2. Library-maintenance previews.
3. Server-configuration snapshots and diffs.
4. Share review and expiry reports.
5. Download-queue management, after a separate API-verification phase.

No mutation command is part of this delivery.

## Command

```text
plexctl history report --mode export|summary|unwatched|inactive \
  [--section SECTION_ID] [--account-id ID] \
  [--older-than DURATION] \
  [--output FILE]
```

`history report` is distinct from the existing `sessions history` command. The latter remains a thin, server-shaped API lookup; the new command provides normalized, deterministic analysis.

### Common flags

- `--section SECTION_ID` limits server history and library-item work to one local PMS section ID. Omitted means all eligible sections.
- `--account-id ID` passes the documented Plex playback-history account filter through to the PMS history endpoint. Omitted means Plex returns the configured account's available history.
- Root `--timeout` applies to every Plex request.
- Root `--json` is not accepted by this command. The output destination extension selects export encoding.
- Any invalid flag combination fails before report output is created or appended.

### Modes

#### `export`

Fetches the documented PMS playback-history endpoint and emits normalized view records in ascending, deterministic order by viewed timestamp, then rating key. A normalized record includes only public media/account metadata already returned by Plex: rating key, title hierarchy, media type, section ID/title, account ID/title when present, and viewed timestamp/duration when present. It never includes credentials, authenticated URLs, request headers, or response bodies.

`--output` is required for `export`.

- `.csv` writes RFC 4180 CSV. If the file is new or empty, write one header; otherwise append records without another header.
- `.jsonl` writes one UTF-8 JSON object per line and appends records.
- Any other suffix, including `.json`, is rejected. JSON arrays are intentionally not supported because appending would make the file invalid.
- A report batch is encoded in memory before opening the destination. The command then opens the destination append-only and writes the complete batch. It must not truncate or replace an existing file. I/O failure returns an error; the command does not claim an all-or-nothing transaction over an arbitrary user-owned append file.
- An empty result appends no data; a new output file is not created for it.

#### `summary`

Computes deterministic aggregate rows from the fetched history:

- grouped by account and library section;
- view count;
- first and most recent viewed timestamps;
- summed view duration when Plex returned a duration.

It prints a stable table to stdout. `--output` is rejected for this first version, avoiding ambiguous multi-row file semantics. It is entirely read-only.

#### `unwatched`

Lists eligible library items with no matching playback-history record.

- The command obtains the selected/all library sections, enumerates items through existing bounded library paging, and compares their stable rating keys to the fetched history set.
- Only normal media items with a stable rating key are considered. Containers, unknown media types, and records with no rating key are skipped rather than guessed.
- Output is deterministic by section title, item title, then rating key.
- `--older-than` is rejected, because `unwatched` means no recorded Plex view ever.
- `--output` is rejected in this first version; output is a table intended for review.

#### `inactive`

Lists eligible library items whose most recent recorded playback is strictly before the calculated cutoff.

- `--older-than DURATION` is required and parsed solely by `time.ParseDuration`; it must be strictly positive. Examples use Go durations such as `2160h`, not `90d`.
- An item with no recorded playback is **not** included. Use `unwatched` for that set.
- The cutoff uses the command's injected/current UTC clock; comparison is strict (`last_viewed_at < cutoff`).
- Output sorting matches `unwatched`.
- `--output` is rejected in this first version.

## Data flow and safety

1. Validate command-specific flags, mode, duration, and output suffix before Plex calls or filesystem writes.
2. Resolve the configured PMS connection and construct the existing authenticated PMS client.
3. Fetch only the documented read endpoints required by the selected mode.
4. Normalize records in memory, apply mode logic, and sort before presentation.
5. Print to stdout, or for `export`, append the encoded batch to the requested `.csv` or `.jsonl` file.

The feature sends only GET requests. It has no local snapshot database, no retry/cancel/delete behavior, no server configuration write, no playlist/share mutation, and no raw-API write escape hatch.

Export files may contain viewing activity and should be treated as user-controlled sensitive local files. `plexctl` does not change permissions of an existing output file. When creating a file, it uses owner-readable/writable permissions subject to the process umask. The command never prints the destination's content to stdout.

## Errors

- Unsupported mode, non-positive/invalid duration, incompatible flag, missing `--output` for export, and unsupported extension fail before Plex calls and before destination creation.
- PMS request, decoding, pagination, or file write errors are returned with context and do not print a success summary.
- A library item or history row missing stable data is skipped only where the mode cannot safely compare it; malformed responses remain errors under the existing client contract.

## Testing

Tests must establish the following observable behavior:

- each mode uses the expected typed read requests and makes no mutation request;
- deterministic ordering regardless of source ordering;
- exact strict-boundary behavior for inactive cutoff;
- unwatched and inactive sets are disjoint under their definitions;
- CSV quoting/header/append behavior and JSONL append behavior;
- rejection of `.json` and unsupported extensions before output creation;
- no output file for empty export and no partial output before validation failures;
- credentials and token sentinels never appear in table/CSV/JSONL/error output;
- existing `sessions history` behavior remains unchanged.

## Documentation

README documents all four modes, the `.csv`/`.jsonl` append-only export contract, the Go duration syntax, viewing-data sensitivity, and the read-only scope.
