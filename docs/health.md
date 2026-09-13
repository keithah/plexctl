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
Kuma URL mapping (`/plex/<account>/<server>` → 200/503 with safe JSON
stage/classification fields, bounded library plus media-byte verification, and
cycle/depth-capped media probes).

`serve` keeps a durable, token-free cache of **previously identity-validated**
PMS connections. A healthy cached endpoint is used before contacting Plex.tv.
When a persisted profile URL exists, it remains an identity-checked fallback
within the three-distinct-endpoint pre-discovery probe budget unless it already
matches a cached candidate; that pre-discovery sequence never spends two probes
on the same URI. It is cached only after validation succeeds. Discovery is used
only after
cache/profile validation fails, and a newly discovered endpoint replaces the
cache only after validation. The cache is an availability optimization, not
blind URL fallback: if the cached endpoint, profile candidate, and fresh
discovery cannot validate the expected machine identifier, the monitor returns
an unhealthy result.
