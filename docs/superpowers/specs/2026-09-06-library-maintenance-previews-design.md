# Library-maintenance previews design

## Goal

Add a read-only `plexctl library maintenance preview` command that identifies four classes of library hygiene candidates without changing Plex or writing local state:

1. empty collections;
2. duplicate titles;
3. missing posters;
4. unmatched items (items without a Plex GUID).

This is a reporting-only PR. Any cleanup, poster refresh, collection deletion, merge, metadata edit, or other mutation remains a separate future typed command with its own review.

## Command surface

```text
plexctl library maintenance preview \
  --mode empty-collections|duplicates|missing-posters|unmatched \
  [--section SECTION_KEY]
```

- `--mode` is required and accepts exactly the four listed values.
- `--section` is optional. When present it restricts the scan to one exact library section key.
- Root `--json` is rejected for this report surface. Standard output is deterministic tabular text.
- The command writes no local output files or databases.

## Read-only request contract

The command sends only context-aware PMS `GET` requests.

- It lists sections with `GET /library/sections/all` unless `--section` is supplied and section metadata is otherwise available to the output.
- It enumerates library items with fully validated offset pagination using `GET /library/sections/{key}/all`.
- Empty-collection mode lists collections with `GET /library/sections/{key}/collections`, then retrieves each collection's items with `GET /library/collections/{collectionID}/items`.
- Missing-poster mode issues a bounded authenticated `GET` byte-range probe against an item's relative `thumb` path when that field is nonempty. It does not download a full image. An absent thumb or a probe that cannot retrieve poster bytes is reported as missing.

There are no POST, PUT, PATCH, DELETE, refresh, scan, metadata-edit, playlist, collection, or sharing requests. Failed or malformed retrievals are errors, not empty candidate lists. Configuration, flag validation, and output schema validation happen before the first PMS request.

## Candidate definitions

### Empty collections

A collection is a candidate only when its collection-item retrieval succeeds and returns exactly zero items. A collection retrieval failure is surfaced as an error and is never labeled empty.

Output rows include section key/title, collection rating key, and collection title. Rows sort by section key, then normalized collection title, then collection rating key.

### Duplicate titles

Items are grouped by normalized title within the same section, regardless of year or media type.

Normalization is deterministic: trim surrounding Unicode whitespace, collapse interior whitespace, and compare case-insensitively. The original public title remains in output. A group is emitted only if it contains at least two distinct items with nonempty rating keys. Containers and items without a stable rating key are excluded.

Output has one row per candidate item, with its section key/title, normalized grouping title, rating key, original title, media type, and year. Groups and rows use deterministic public-field tie-breakers.

### Missing posters

A normal media item with a nonempty rating key is a candidate when its `thumb` field is empty or an authenticated bounded byte-range probe of that relative thumb path does not yield usable bytes. Probe errors are classified as missing only after the item listing itself has completed successfully.

Output includes section key/title, rating key, title, media type, and `reason` (`missing_thumb` or `probe_failed`). It never prints raw thumb URLs, server base URLs, tokens, headers, or response bodies. The report fails closed for invalid thumbnail paths rather than resolving an absolute or external URL.

### Unmatched items

A normal media item with a nonempty rating key is a candidate when Plex supplies no nonempty GUID values for that item. An empty `Guid` array and arrays whose values are all empty both count as unmatched.

Output includes section key/title, rating key, title, media type, and year. It never prints GUID values from matched items.

## Media eligibility

The library reports analyze normal media types only: `movie`, `show`, `season`, `episode`, `artist`, `album`, `track`, `photo`, and `clip`. Collections, directories, and unknown or empty types are excluded from item-based candidate modes. Empty-collection mode evaluates collection resources themselves as described above.

## Pagination and integrity

Every paged item enumeration must require explicit response `offset` and `totalSize`, ensure the offset matches the requested start, ensure declared size equals decoded item count, reject a decreased total or a total smaller than accumulated results, permit a growing total, and fail on no progress. The command never presents an aggregate as complete if any invariant fails.

## Errors, privacy, and determinism

- PMS transport errors retain safe method/path context but redact server base URLs, tokens, and headers.
- Candidate rows are sorted before printing; upstream order does not affect results.
- No report rows print credentials, media bytes, complete image URLs, response bodies, or unbounded metadata.
- The command prints no partial output before all prerequisite retrieval and analysis for that mode succeeds. Poster probe failures are candidate data, not fatal output errors, only after a complete validated item set exists.

## Tests and acceptance

Tests must cover strict flag validation before requests, GET-only request logs, happy paths for all four modes, section scoping, deterministic ordering, normal-media eligibility, no-GUID detection, duplicate normalization, empty collection retrieval errors, poster absent/probe-success/probe-failure cases, safe relative-path enforcement, paged enumeration failure propagation, and output redaction.

Fixture-backed CLI acceptance must verify all report requests are GET and prove the command performs no Plex mutation or local persistent write.
