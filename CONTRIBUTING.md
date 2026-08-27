# Contributing

Run `gofmt`, `go test ./...`, `go vet ./...`, `go build ./cmd/plexctl`, and both
Python repository checks before submitting changes. Do not include Plex tokens,
private hostnames, credentials, or token-bearing URLs in fixtures or examples.
Changes to the pinned API contract must include its source/version/checksum and
an updated operation inventory.
