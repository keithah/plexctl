# Health checks

`plexctl health ping` checks the PMS identity endpoint. `plexctl health check`
checks identity and library access within a bounded context deadline. Results
include a stable stage and classification and can be serialized with `--json`.

Authentication failures (HTTP 401/403), timeouts, identity failures, and
library failures have distinct classifications. A successful request remains
healthy even if its context is cancelled immediately after the response is
received. Error details are bounded and redact Plex tokens before truncation.

The health package is deliberately independent of Uptime Kuma and Yarr. The
repository does not currently ship an MCP server or HTTP adapter. The planned
Kuma integration is a thin local HTTP wrapper around this package, preserving
the existing monitor URL shape while avoiding terminal-output parsing or a
second Plex implementation. See [Monitoring integration](monitoring.md).
