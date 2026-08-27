# Health checks

`plexctl health ping` checks the PMS identity endpoint. `plexctl health check`
checks identity and library access within a bounded context deadline. Results
include a stable stage and classification and can be serialized with `--json`.

The health package is deliberately independent of Uptime Kuma and Yarr. A
future service adapter can import it and preserve an existing monitor URL while
using structured results rather than parsing terminal output.
