# Monitoring integration
## Current state
`plexctl` ships a Go CLI, reusable client, and an optional HTTP monitoring
adapter. It does **not** ship an MCP server. Uptime Kuma currently monitors the
**new** `plexctl-monitor:3003` service, whose URLs look like:
```text
http://plexctl:3003/plex/<account>/<server>
```

No live Kuma monitor is changed by this document.

The adapter is started by the CLI:

```bash
plexctl serve --listen 0.0.0.0:3003 --timeout 10s
```

For a container deployment, mount the `plexctl` configuration and provide the
OS-keychain integration or deployment-specific credential setup required by
the selected account. Prefer binding to the private Docker/network interface;
`127.0.0.1` is the secure default.

Configure each Kuma HTTP monitor to use the corresponding adapter URL, for
example:

```text
http://plexctl:3003/plex/keithah/SF2
```

Set the expected status to `200` and use a reasonable monitor timeout longer
than the adapter's upstream deadline. Kuma will treat the adapter's `503` as a
failure while retaining the JSON body in its monitor history.

The implementation imports `internal/health` and the same
configuration/authentication layers used by the CLI. The adapter is a
monitor-facing compatibility surface, not a second Plex client.

For each configured account/server target, expose the existing URL shape:

```text
GET /plex/<account>/<server>
```

The handler should:

1. Resolve the named account and server from the persisted configuration.
2. Resolve credentials only at runtime from the OS credential store.
3. Use the already-selected, validated connection URL; never select a private
   or stale discovered URL blindly.
4. Run the bounded deep health check.
5. Return a small, safe JSON response with the server name, stage,
   classification, duration, and redacted detail.
6. Return HTTP 200 only for a healthy result. Return HTTP 503 for an unhealthy
   result, with 401/403, timeout, identity, and library classifications retained
   in the body for diagnosis.

The adapter should bind to loopback or a private interface by default. It must
not expose Plex tokens, credential values, authenticated URLs, or full upstream
responses. A reverse proxy can publish it to Kuma if Kuma cannot reach the
private bind address directly.

Example healthy response shape (illustrative; the exact schema is versioned
in the adapter tests):

```json
{
  "ok": true,
  "account": "keithah",
  "server": "SF2",
  "stage": "library",
  "classification": "ok",
  "duration_ms": 142
}
```

## Why not monitor Plex directly from Kuma?

A direct Kuma HTTP monitor is useful for a public, unauthenticated endpoint,
but it is the wrong replacement for these checks because it would have to
reimplement or bypass:

- Plex account-to-server discovery;
- per-server credential lookup;
- local/direct/relay connection preference;
- bounded identity-plus-library health semantics; and
- safe classification of authentication failures versus outages.

It would also make every monitor carry its own Plex URL and credential policy.
The adapter keeps those decisions in one tested codebase.

## Migration plan

1. Implement and test the adapter without changing Kuma.
2. Run it beside `plex-monitor` and compare results for every active Plex
   monitor, preserving the existing account/server mapping.
3. Add one disabled or canary Kuma HTTP monitor pointing at the adapter.
4. Verify identity and deep-health behavior, HTTP statuses, timeout handling,
   token redaction, and the monitor's recent heartbeats.
5. Migrate one active monitor, then read it back and observe it through a full
   failure/recovery window.
6. Migrate the remaining monitors in small batches.
7. Retire `plex-monitor` only after all required monitor URLs have equivalent
   adapter coverage and rollback is no longer needed.

Do not replace all monitors with one fleet aggregate unless the desired alert
semantics are explicitly changed. Per-server monitors preserve isolation and
make the affected Plex instance immediately visible in Kuma. A separate
fleet-summary endpoint can be added later for an at-a-glance dashboard, but it
should not replace per-server failure signals.

## Alternatives

### CLI subprocess wrapper

Keep a small HTTP service that invokes `plexctl health check --json` as a
subprocess. This is a reasonable first spike, but it adds process startup and
JSON/exit-code translation, and it makes the adapter depend on CLI formatting.
It is acceptable as a temporary compatibility bridge, not the preferred final
design.

### Direct Kuma monitors

Point each monitor at a Plex URL and supply its token in Kuma. This has the
smallest initial code change but duplicates discovery/configuration, increases
secret exposure, and cannot reproduce the corrected connection selection.
Reject this approach for the migration.

## Relationship to MCP and Yarr

MCP is not needed for Kuma monitoring. Kuma needs a synchronous HTTP health
contract; an MCP server would be an operator/agent interface and would add
latency and another transport. The adapter should share the health core with
the CLI and remain independent of Yarr. A future MCP integration, if desired,
can expose the same typed health results without becoming a runtime dependency
of the monitor path.