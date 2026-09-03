# External Plex Sharing Design

## Scope

Add external Plex friend/server sharing management to `plexctl` only. This is independent of the Printing Press catalog and will be delivered through a PR to `keithah/plexctl`.

The feature manages external Plex accounts invited to share a server. Plex Home and managed users are explicitly out of scope because they use a different API and permission model.

## Goals

- List external shared users with Plex username, email when returned by Plex, nested server-share state, and per-server library grants. Pending outgoing invites are listed separately when Plex returns them.
- List eligible library sections for a selected owned server before a grant is created or replaced.
- Invite an external account by email address or Plex username.
- Replace an existing external share's library grants and sharing settings.
- Revoke an external share only after explicit confirmation.
- Reuse the existing protected Plex.tv credential and fresh resource discovery path. Tokens and passwords must never be command-line arguments, written to output, or persisted by this feature.

## Non-goals

- Plex Home/managed-user creation, membership, PINs, restrictions, or permissions.
- Local PMS user-play-history endpoints beyond the existing CLI behavior.
- Raw arbitrary Plex.tv HTTP routing.
- Bulk invitations, bulk changes, or bulk removals.
- Changes to the Printing Press catalog, PR #1903, registry, or generated skills.

## Command Surface

Commands are placed under the existing root command:

```text
plexctl sharing users [--server <name-or-client-identifier>]
plexctl sharing libraries --server <name-or-client-identifier>
plexctl sharing invite <email-or-username> --server <name-or-client-identifier> (--libraries <id,id,...> | --all-libraries)
plexctl sharing update <share-id> --server <name-or-client-identifier> (--libraries <id,id,...> | --all-libraries)
plexctl sharing remove <share-id> --server <name-or-client-identifier> --yes
```

`sharing users` is read-only and returns a stable JSON shape with account identity, email when present, nested-share pending state, share ID, target-server identity, and grants. Pending outgoing invitations come from Plex's separate requested-invites response and remain distinct from established accounts. Human output is a concise table. Missing email is represented as absent/null; the CLI must not infer or synthesize one.

`sharing libraries` is read-only and returns the Plex.tv library-section IDs and names associated with the selected owned server. It exists to make mutation input auditable and avoid using local, stale, or guessed section IDs.

`invite` accepts exactly one unambiguous external identifier. It creates an external server share using the selected owned server and requested section grants. `update` replaces the grant set rather than merging it. `remove` revokes precisely one server-share link.

The first release manages library access only. It reports any Plex-provided sharing settings in `sharing users` output but does not expose setting-edit flags until their live response and mutation semantics are independently verified. Unsupported flags fail as usage errors rather than being silently ignored.

## Identity and Resolution

The implementation uses the existing Plex.tv client and stored `PLEXCTL_USER_TOKEN` resolution. A selected server must be discovered fresh from Plex.tv resources and be owned by the authenticated account.

Resolution accepts an exact `clientIdentifier` or an exact server name. A name matching zero or multiple owned resources fails with candidates and no network mutation. The stable `clientIdentifier` is included in all outputs and requests.

Shares are addressed by Plex.tv's server-share identifier, not an email or display name. The CLI lists that identifier so an operator can select an exact target. It does not resolve ambiguous human identities for update or removal.

## Plex.tv Client Boundary

Extend `internal/plexauth`, the existing protected Plex.tv credential and resource-discovery boundary, with a narrow typed sharing surface:

- `ListUsers(ctx)` from `GET /api/users/` (XML)
- `ListRequestedInvites(ctx)` from `GET /api/invites/requested` (XML)
- `ListServerLibraries(ctx, server)` from `GET /api/servers/{machineIdentifier}` (XML)
- `ListShareSections(ctx, server, shareID)` from `GET /api/servers/{machineIdentifier}/shared_servers/{shareID}` (XML)
- `Invite(ctx, request)`, `Update(ctx, request)`, and `Remove(ctx, server, shareID)` using the separately verified typed mutation paths

The boundary applies current repository conventions: `X-Plex-Token`, standard Plex client headers, context propagation, bounded response reads, typed HTTP/status errors, and adaptive rate limiting. It validates response shape before presenting data.

The shared-users response is XML and each nested `Server.id` is the share ID; it must never be confused with the distinct `Server.serverId`. Library grants are read from the per-share detail endpoint. Endpoint/body details are confirmed against maintained-client behavior and real read-only account data before mutations are implemented; request-shape tests use `httptest`.

## Safety and MCP Policy

- `sharing users` and `sharing libraries` are MCP read-only commands.
- `sharing invite`, `sharing update`, and `sharing remove` are remote mutations and must not be marked read-only or local-write.
- The remove command requires `--yes`; without it the command returns an actionable error and makes no HTTP request.
- Invite and update require exactly one of `--libraries` and `--all-libraries`. An empty library list is rejected before any HTTP request.
- Update must state in its human output that the supplied set replaces the current grants.
- The selected owned server, share ID or external target, requested libraries, and non-default settings appear in dry-run/confirmation output. `--dry-run` makes no remote call.
- The CLI never prints access tokens or request Authorization headers. Existing response redaction applies to diagnostics.

## Error Handling

- Missing credential: existing authentication error path and no request.
- No matching or ambiguous server: usage error listing candidate client identifiers; no request.
- A selected resource that is not owned: authorization/usage error; no mutation.
- Unknown library IDs: reject before mutation when absent from the selected server's Plex.tv library list.
- Existing invite/conflicting share (`409`/`422`): typed actionable error with no retry as a mutation.
- `401`/`403`: credential/authorization error; never reclassify as an empty user list.
- `429`: preserve Retry-After behavior and typed rate-limit error.
- Malformed or oversized Plex.tv responses: fail closed with no partial mutation inference.

## Tests and Verification

Use TDD in vertical slices:

1. A failing `httptest` client test for listing users, followed by client implementation.
2. A failing CLI test for server/libraries resolution and read-only JSON output, followed by wiring.
3. A failing invite test that asserts exact endpoint/body and rejects invalid grant selections before an HTTP call.
4. A failing update test proving replacement semantics and exact server/share targeting.
5. A failing removal test proving `--yes` is required and no request occurs without it.
6. Error-path tests for ambiguous/non-owned servers, missing grants, malformed data, and 401/403/422/429 responses.

Run focused package tests after each slice, then `go test ./... -count=1`, `go vet ./...`, `go build ./...`, `gofmt -l .`, and `git diff --check`.

Before opening the Plexctl PR, perform a read-only live verification of `sharing users` and `sharing libraries` using the configured Plex account. Parse the returned fields and record only non-secret aggregate evidence. Mutating live verification requires an explicitly designated disposable external test account and is not performed by default.

## Documentation

Update `README.md` and relevant CLI help with the external-only boundary, exact grant replacement behavior, `--yes` removal safeguard, and the read-only live-verification boundary. Do not document unsupported sharing-settings flags.
