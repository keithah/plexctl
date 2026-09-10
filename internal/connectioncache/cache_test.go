package connectioncache

import (
	"path/filepath"
	"testing"

	"github.com/keithah/plexctl/internal/plexauth"
)

func TestStorePersistsValidatedConnectionAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	store := New(path)
	want := plexauth.Connection{URI: "https://server.plex.direct:32400", Protocol: "https", Local: false, Relay: false}
	if err := store.Put("account", "machine", want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := New(path).Get("account", "machine")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("persisted connection was not found")
	}
	if got != want {
		t.Fatalf("connection=%+v, want %+v", got, want)
	}
}

func TestStoreRetainsThreeMostRecentDistinctValidatedConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connections.json")
	store := New(path)
	connections := []plexauth.Connection{
		{URI: "https://one.example:32400"},
		{URI: "https://two.example:32400"},
		{URI: "https://three.example:32400"},
		{URI: "https://four.example:32400"},
	}
	for _, connection := range connections {
		if err := store.Put("account", "machine", connection); err != nil {
			t.Fatal(err)
		}
	}

	got, err := New(path).Candidates("account", "machine")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://four.example:32400", "https://three.example:32400", "https://two.example:32400"}
	if len(got) != len(want) {
		t.Fatalf("candidate count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].URI != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i].URI, want[i])
		}
	}
}

func TestStoreSeparatesAccountsWithSameMachineIdentifier(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "connections.json"))
	first := plexauth.Connection{URI: "https://first.plex.direct:32400"}
	second := plexauth.Connection{URI: "https://second.plex.direct:32400"}
	if err := store.Put("first-account", "machine", first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("second-account", "machine", second); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.Get("second-account", "machine")
	if err != nil || !ok || got != second {
		t.Fatalf("Get(second-account) = %+v, %t, %v; want %+v, true, nil", got, ok, err, second)
	}
}

func TestStoreRejectsIncompleteCacheKeysAndConnections(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "connections.json"))
	for _, tc := range []struct {
		account string
		machine string
		conn    plexauth.Connection
	}{
		{"", "machine", plexauth.Connection{URI: "https://server.plex.direct:32400"}},
		{"account", "", plexauth.Connection{URI: "https://server.plex.direct:32400"}},
		{"account", "machine", plexauth.Connection{}},
	} {
		if err := store.Put(tc.account, tc.machine, tc.conn); err == nil {
			t.Fatalf("Put(%q, %q, %+v) succeeded", tc.account, tc.machine, tc.conn)
		}
	}
}
