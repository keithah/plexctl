package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestCommandTreeIsRegistered(t *testing.T) {
	want := []string{"auth login", "accounts list", "accounts use", "servers list", "servers use", "server info", "library search", "library recently-added", "metadata children", "sessions list", "sessions history", "playlists list", "playlists get", "playlists items", "collections list", "collections items", "download-queues get", "download-queues items", "download-queues item", "download-queues decision", "transcode decision", "transcode subtitles"}
	for _, path := range want {
		parts := strings.Split(path, " ")
		cmd, _, err := NewRoot().Find(parts)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if cmd.Name() != parts[len(parts)-1] {
			t.Fatalf("%s resolved to %q", path, cmd.CommandPath())
		}
	}
}

// search and recently-added must not share one --limit variable, otherwise
// setting the flag on one command leaks into the other.
func TestLimitFlagsAreIndependent(t *testing.T) {
	root := NewRoot()
	search, _, err := root.Find([]string{"library", "search"})
	if err != nil {
		t.Fatal(err)
	}
	recent, _, err := root.Find([]string{"library", "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if err = search.Flags().Set("limit", "3"); err != nil {
		t.Fatal(err)
	}
	if got := recent.Flags().Lookup("limit").Value.String(); got != "20" {
		t.Fatalf("recently-added --limit leaked: %s", got)
	}
	if search.Flags().Lookup("section") == nil {
		t.Fatal("search is missing --section")
	}
}

func TestRawAPIRejectsMutations(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		if _, err := run(t, "api", method, "/identity"); err == nil {
			t.Fatalf("%s was not rejected", method)
		} else if !strings.Contains(err.Error(), "typed command") {
			t.Fatalf("%s: unexpected error %v", method, err)
		}
	}
}

// The raw API command previously advertised a --body-json flag that was never
// sent; a flag that silently does nothing must not exist.
func TestRawAPIHasNoInertBodyFlag(t *testing.T) {
	cmd, _, err := NewRoot().Find([]string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if f := cmd.Flags().Lookup("body-json"); f != nil {
		t.Fatal("api still exposes an inert --body-json flag")
	}
}

func TestUnknownServerIsReported(t *testing.T) {
	t.Setenv("PLEXCTL_CONFIG", t.TempDir()+"/config.json")
	_, err := run(t, "server", "identity", "--server", "missing")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestArgumentValidation(t *testing.T) {
	t.Setenv("PLEXCTL_CONFIG", t.TempDir()+"/config.json")
	for _, args := range [][]string{{"library", "search"}, {"metadata", "children"}, {"library", "recently-added"}} {
		if _, err := run(t, args...); err == nil {
			t.Fatalf("%v accepted missing arguments", args)
		}
	}
}

func TestAccountAndServerCommandsListByDefault(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"accounts", "servers"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		if cmd.RunE == nil {
			t.Fatalf("%s should list when invoked without a subcommand", name)
		}
	}
}

func TestDownloadQueueCommandsAcceptDocumentedArguments(t *testing.T) {
	root := NewRoot()
	for _, path := range []string{"get", "items"} {
		cmd, _, err := root.Find([]string{"download-queues", path})
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Args(cmd, []string{"1"}); err != nil {
			t.Fatalf("download-queues %s rejected QUEUE_ID: %v", path, err)
		}
	}
}

func TestBestConnectionPrefersLocalDirect(t *testing.T) {
	got := bestConnection([]plexauth.Connection{
		{URI: "http://172.18.0.2:32400", Local: true},
		{URI: "http://203.0.113.10:32400", Local: false},
		{URI: "https://relay.plex.tv", Relay: true},
	})
	if got.URI != "http://172.18.0.2:32400" {
		t.Fatalf("selected %q, want local direct connection", got.URI)
	}
}

func TestNormalizeDiscoveredConnectionUsesHTTPSForRemoteHTTP(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "http://203.0.113.10:32400"})
	if got.URL != "https://203.0.113.10:32400" || !got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want HTTPS with insecure TLS", got)
	}
}

func TestNormalizeDiscoveredConnectionDisablesTLSVerificationForPortlessIP(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "http://203.0.113.10"})
	if got.URL != "https://203.0.113.10" || !got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want HTTPS with insecure TLS", got)
	}
}

func TestNormalizeDiscoveredConnectionDisablesTLSVerificationForPortlessIPv6(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "http://[2001:db8::10]"})
	if got.URL != "https://[2001:db8::10]" || !got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want IPv6 HTTPS with insecure TLS", got)
	}
}

func TestNormalizeDiscoveredConnectionKeepsHostnameTLSVerification(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "http://server.plex.direct:32400"})
	if got.URL != "https://server.plex.direct:32400" || got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want verified HTTPS for hostname", got)
	}
}

func TestNormalizeDiscoveredConnectionPreservesLocalHTTP(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "http://192.168.1.10:32400", Local: true})
	if got.URL != "http://192.168.1.10:32400" || got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want local HTTP without insecure TLS", got)
	}
}

func TestNormalizeDiscoveredConnectionDisablesTLSVerificationForHTTPSIPLiteral(t *testing.T) {
	// A certificate can never match a bare IP, so a discovered https:// IP
	// endpoint needs the same treatment as an upgraded http:// one.
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "https://203.0.113.10:32400"})
	if got.URL != "https://203.0.113.10:32400" || !got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want insecure TLS for https IP literal", got)
	}
}

func TestNormalizeDiscoveredConnectionKeepsHTTPSHostnameVerification(t *testing.T) {
	got := normalizeDiscoveredConnection(plexauth.Connection{URI: "https://server.plex.direct:32400"})
	if got.URL != "https://server.plex.direct:32400" || got.InsecureTLS {
		t.Fatalf("normalized connection=%+v, want verified HTTPS for hostname", got)
	}
}

func TestOrderedConnectionsRanksRelayLast(t *testing.T) {
	ordered := orderedConnections([]plexauth.Connection{
		{URI: "https://relay.plex.direct:443", Relay: true},
		{URI: "https://direct.plex.direct:32400"},
		{URI: "http://10.0.0.5:32400", Local: true},
	})
	want := []string{"http://10.0.0.5:32400", "https://direct.plex.direct:32400", "https://relay.plex.direct:443"}
	if len(ordered) != len(want) {
		t.Fatalf("ordered %d connections, want %d", len(ordered), len(want))
	}
	for i, w := range want {
		if ordered[i].URI != w {
			t.Fatalf("ordered[%d]=%q, want %q (relay must be probed last)", i, ordered[i].URI, w)
		}
	}
}

func TestOrderedConnectionsSkipsEmptyURIs(t *testing.T) {
	ordered := orderedConnections([]plexauth.Connection{{URI: ""}, {URI: "https://direct.plex.direct:32400"}})
	if len(ordered) != 1 || ordered[0].URI != "https://direct.plex.direct:32400" {
		t.Fatalf("ordered=%+v, want only the non-empty connection", ordered)
	}
}

func TestValidatedConnectionFallsBackToRelayWhenDirectIdentityMismatches(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"other"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer direct.Close()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"expected"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer relay.Close()
	got, err := validatedConnection(context.Background(), plexauth.Resource{
		ClientIdentifier: "expected",
		AccessToken:      "server-token",
		Connections: []plexauth.Connection{
			{URI: direct.URL, Local: true, Relay: false},
			{URI: relay.URL, Local: false, Relay: true},
		},
	}, "account-token")
	if err != nil {
		t.Fatal(err)
	}
	if got.URI != relay.URL {
		t.Fatalf("selected %q, want relay %q", got.URI, relay.URL)
	}
}

func TestValidatedConnectionFailsClosedWhenNoIdentityMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"other"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	_, err := validatedConnection(context.Background(), plexauth.Resource{
		ClientIdentifier: "expected",
		Connections:      []plexauth.Connection{{URI: server.URL, Local: true}},
	}, "account-token")
	if err == nil {
		t.Fatal("validatedConnection returned a connection after all identities failed")
	}
}

func TestConfiguredProfileAppliesPersistedInsecureTLS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer ts.Close()
	client, err := newPMSClient(config.Server{URL: ts.URL, InsecureTLS: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := client.Identity(context.Background())
	if err != nil || identity.MediaContainer.MachineIdentifier != "m1" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
}
