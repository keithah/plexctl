package cli

import (
	"testing"

	"github.com/keithah/plexctl/internal/plexauth"
)

// Server profile keys must be stable across logins. A positional fallback key
// makes an unrelated profile get overwritten when Plex returns the same
// resources in a different order.
func TestProfileKeyIsStableWhenClientIdentifierIsMissing(t *testing.T) {
	alpha := plexauth.Resource{Name: "Alpha", Connections: []plexauth.Connection{{URI: "http://alpha:32400", Local: true}}}
	beta := plexauth.Resource{Name: "Beta", Connections: []plexauth.Connection{{URI: "http://beta:32400", Local: true}}}

	firstAlpha := profileKey("acct", alpha, 0)
	firstBeta := profileKey("acct", beta, 1)

	// Same account, same servers, reversed discovery order.
	secondBeta := profileKey("acct", beta, 0)
	secondAlpha := profileKey("acct", alpha, 1)

	if firstAlpha != secondAlpha {
		t.Errorf("Alpha key changed with discovery order: %q then %q", firstAlpha, secondAlpha)
	}
	if firstBeta != secondBeta {
		t.Errorf("Beta key changed with discovery order: %q then %q", firstBeta, secondBeta)
	}
	if firstAlpha == firstBeta {
		t.Fatalf("distinct servers collided on key %q", firstAlpha)
	}
}

// The machine identifier remains the key whenever Plex provides one.
func TestProfileKeyPrefersClientIdentifier(t *testing.T) {
	r := plexauth.Resource{Name: "Alpha", ClientIdentifier: "machine-1"}
	if got := profileKey("acct", r, 3); got != "machine-1" {
		t.Fatalf("key=%q, want the client identifier", got)
	}
}

// Connection preference order is the contract discovery relies on.
func TestOrderedConnectionsRanksLocalThenDirectThenRelay(t *testing.T) {
	got := orderedConnections([]plexauth.Connection{
		{URI: "https://relay.plex.tv", Relay: true},
		{URI: "http://203.0.113.10:32400"},
		{URI: "http://172.18.0.2:32400", Local: true},
		{URI: ""},
	})
	want := []string{"http://172.18.0.2:32400", "http://203.0.113.10:32400", "https://relay.plex.tv"}
	if len(got) != len(want) {
		t.Fatalf("got %d connections, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].URI != w {
			t.Errorf("position %d = %q, want %q", i, got[i].URI, w)
		}
	}
}
