# Health checks

`plexctl health ping` checks the PMS identity endpoint. `plexctl health check`
checks identity and library access within a bounded context deadline. Results
include a stable stage and classification and can be serialized with `--json`.

Authentication failures (HTTP 401/403), timeouts, identity failures, and
library failures have distinct classifications. A successful request remains
healthy even if its context is cancelled immediately after the response is
received. Error details are bounded and redact Plex tokens before truncation.

The health package is deliberately independent of Uptime Kuma and Yarr. The
repository does not currently ship an MCP server. The current monitor adapter
(running at `plexctl-monitor:3003`) replaces the retired `plex-monitor:3002`
service; Kuma monitors point at the adapter URL, not at a Plex URL directly.
See [Monitoring integration](monitoring.md) for the adapter contract and the
Kuma URL mapping (`/plex/<account>/<server>` → `200` when healthy, `503` for
health or dependency failures, and safe JSON stage/classification fields on all
responses, including `404` request/configuration, `405` method, and `500`
handler-state failures).

`serve` keeps a durable, token-free cache of **previously identity-validated**
PMS connections. A healthy cached endpoint is used before contacting Plex.tv.
For a profile with a stable machine identifier, a persisted profile URL remains
an identity-checked fallback within the three-distinct-endpoint pre-discovery
probe budget unless it already matches a cached candidate; that pre-discovery
sequence never spends two probes on the same URI. It is cached only after
validation succeeds. Discovery is used only after
cache/profile validation fails, and a newly discovered endpoint replaces the
cache only after validation. Profiles without a stable machine identifier skip
identity-validated cache/profile probing; discovery selects their configured
profile name, falling back to the configured server key, and does not persist a
durable connection-cache entry. The cache is an availability optimization, not
blind URL fallback: if the cached endpoint, profile candidate, and fresh
discovery cannot validate the expected machine identifier, the monitor returns
an unhealthy result.

Each adapter request waits under its own deadline for resolution and health
checks. The handler's shared selector-resolution worker has a separate
30-second upper bound. When its last deadline-bound waiter expires, the handler
cancels that unresolved worker; a short or canceled leader cannot terminate
discovery needed by a healthy waiter. The independently bounded Plex resource
cache refresh intentionally does not inherit an individual waiter's
cancellation, so it can finish a reusable discovery snapshot. A classified
transient discovery failure can make one additional resolution attempt after a
one-second delay only while that request deadline remains active. Configuration
failures, cancellation, and expired requests never retry.
Retry logs contain only a stable outcome and attempt number, never a selected
account/server, endpoint, credential, or raw upstream error.
