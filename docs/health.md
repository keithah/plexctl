# Health checks

`plexctl health ping` checks the PMS identity endpoint. `plexctl health check`
checks identity and library access within a bounded context deadline. Results
include a stable stage and classification and can be serialized with `--json`.

Authentication failures (HTTP 401/403), timeouts, identity failures, and
library failures have distinct classifications. A successful request remains
healthy even if its context is cancelled immediately after the response is
received. Error details are bounded and redact Plex tokens before truncation.

The health package is deliberately independent of Uptime Kuma and Yarr. The
repository does not currently ship an MCP server or HTTP adapter. The current
monitor adapter (running at `plexctl-monitor:3003`) replaces the retired
`plex-monitor:3002` service; Kuma monitors point at the adapter URL, not
at a Plex URL directly. See [Monitoring integration](monitoring.md) for the
adapter contract and the Kuma URL mapping (`/plex/<account>/<server>` → 200/503
with redacted JSON detail, bounded library plus media-byte verification, and
cycle/depth-capped media probes). Cache (30s TTL, keyring-file-keyed, fail-closed)
is wired into `serve` but does not replace fresh discovery.
