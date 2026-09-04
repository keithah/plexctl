package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/connectioncache"
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
	want := []string{"auth login", "accounts list", "accounts use", "servers list", "servers use", "server info", "library search", "library recently-added", "metadata children", "sessions list", "sessions history", "playlists list", "playlists get", "playlists items", "collections list", "collections items", "download-queues get", "download-queues items", "download-queues item", "download-queues decision", "transcode decision", "transcode subtitles", "sharing users", "sharing libraries"}
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

func TestResolveCachedServeTargetTreatsUnreadableCacheAsMiss(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "connections.json")
	if err := os.WriteFile(cachePath, []byte(`not-json`), 0600); err != nil {
		t.Fatal(err)
	}
	_, ok, err := resolveCachedServeTarget(context.Background(), connectioncache.New(cachePath), "account", config.ServerProfile{MachineIdentifier: "machine"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unreadable cache was selected")
	}
}

func TestCachedServeTokenPrefersServerSpecificCredential(t *testing.T) {
	t.Setenv("PLEXCTL_TOKEN_SERVER_ACCOUNT_MACHINE", "server-token")
	got, err := cachedServeToken(config.ServerProfile{TokenKey: "server/account/machine"}, "account-token")
	if err != nil {
		t.Fatal(err)
	}
	if got != "server-token" {
		t.Fatalf("token=%q, want server-specific token", got)
	}
}

func TestCachedServeTokenFallsBackToAccountCredential(t *testing.T) {
	got, err := cachedServeToken(config.ServerProfile{}, "account-token")
	if err != nil {
		t.Fatal(err)
	}
	if got != "account-token" {
		t.Fatalf("token=%q, want account token", got)
	}
}

func TestResolveCachedServeTargetUsesValidatedEndpointWithoutDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/identity" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine"}}`))
	}))
	defer server.Close()

	cache := connectioncache.New(filepath.Join(t.TempDir(), "connections.json"))
	if err := cache.Put("account", "machine", plexauth.Connection{URI: server.URL, Local: true}); err != nil {
		t.Fatal(err)
	}
	client, ok, err := resolveCachedServeTarget(context.Background(), cache, "account", config.ServerProfile{MachineIdentifier: "machine"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("valid cached endpoint was not selected")
	}
	identity, err := client.Identity(context.Background())
	if err != nil || identity.MediaContainer.MachineIdentifier != "machine" {
		t.Fatalf("identity=%+v err=%v", identity, err)
	}
}

func TestResolveCachedServeTargetSeedsCacheFromValidatedProfileURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine"}}`))
	}))
	defer server.Close()

	cache := connectioncache.New(filepath.Join(t.TempDir(), "connections.json"))
	profile := config.ServerProfile{MachineIdentifier: "machine", URL: server.URL, Local: true}
	client, ok, err := resolveCachedServeTarget(context.Background(), cache, "account", profile, "token")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("validated persisted profile URL was not selected")
	}
	if _, err := client.Identity(context.Background()); err != nil {
		t.Fatal(err)
	}
	connection, found, err := cache.Get("account", "machine")
	if err != nil || !found || connection.URI != server.URL {
		t.Fatalf("cache.Get = %+v, %t, %v; want %q, true, nil", connection, found, err, server.URL)
	}
}

func TestResolveCachedServeTargetFallsBackWhenIdentityNoLongerMatches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"other"}}`))
	}))
	defer server.Close()

	cache := connectioncache.New(filepath.Join(t.TempDir(), "connections.json"))
	if err := cache.Put("account", "machine", plexauth.Connection{URI: server.URL, Local: true}); err != nil {
		t.Fatal(err)
	}
	_, ok, err := resolveCachedServeTarget(context.Background(), cache, "account", config.ServerProfile{MachineIdentifier: "machine"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("cache selected an endpoint with the wrong machine identifier")
	}
}
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Listings iterate maps, so they must be sorted to stay stable across runs.
func TestAccountAndServerListingsAreSortedAndStable(t *testing.T) {
	c := config.Config{
		CurrentAccount: "bob",
		CurrentServer:  "s2",
		Accounts: map[string]config.Account{
			"zoe":   {Email: "zoe@example.com"},
			"alice": {Email: "alice@example.com"},
			"bob":   {Email: "bob@example.com"},
		},
		ServersV2: map[string]config.ServerProfile{
			"s3": {Name: "Three", URL: "http://3", Account: "zoe"},
			"s1": {Name: "One", URL: "http://1", Account: "alice"},
			"s2": {Name: "Two", URL: "http://2", Account: "bob"},
		},
	}
	accounts := captureStdout(t, func() { printAccounts(c) })
	servers := captureStdout(t, func() { printServers(c) })
	for i := 0; i < 25; i++ {
		if got := captureStdout(t, func() { printAccounts(c) }); got != accounts {
			t.Fatalf("account listing is not deterministic:\n%q\nvs\n%q", accounts, got)
		}
		if got := captureStdout(t, func() { printServers(c) }); got != servers {
			t.Fatalf("server listing is not deterministic:\n%q\nvs\n%q", servers, got)
		}
	}
	wantAccounts := "alice\talice@example.com\nbob\tbob@example.com *\nzoe\tzoe@example.com\n"
	if accounts != wantAccounts {
		t.Errorf("accounts = %q, want %q", accounts, wantAccounts)
	}
	wantServers := "s1\tOne\thttp://1\taccount=alice\ns2\tTwo\thttp://2\taccount=bob *\ns3\tThree\thttp://3\taccount=zoe\n"
	if servers != wantServers {
		t.Errorf("servers = %q, want %q", servers, wantServers)
	}
}
