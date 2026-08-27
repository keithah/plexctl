# API coverage

The authoritative contract is the normalized JSON file in this directory.
Refresh it from <https://developer.plex.tv/pms/> by extracting the embedded
`__redoc_state.spec.data` document, then update the checksum and review the
operation inventory. The current pinned contract contains 205 paths and 258
operations.

The low-level `plexctl api GET /path` command can reach any read operation in
the contract. Curated typed wrappers are being expanded by API family; the
coverage check prevents accidental contract shrinkage while wrappers evolve.
