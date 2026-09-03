package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
)

func TestSharingUsersJSONShowsNestedShareStateAndGrants(t *testing.T) {
	server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="owned-server" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User username="home-user" home="1"><Server id="1" serverId="1" machineIdentifier="owned-server" name="Owned Server"/></User><User username="friend" email="friend@example.com"><Server id="98" serverId="122" machineIdentifier="foreign-server" name="Foreign Server" allLibraries="0" pending="0" owned="0"/><Server id="99" serverId="123" machineIdentifier="owned-server" name="Owned Server" allLibraries="0" pending="1" owned="1"/></User></MediaContainer>`))
		case "/api/servers/owned-server/shared_servers/99":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" shared="1" title="Movies" type="movie"/></SharedServer>`))
		default:
			http.NotFound(w, r)
		}
	})
	configureSharingTestAccount(t, "alice", "", "", "account-token")
	useSharingPlexServer(t, server)

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "users", "--json") })
	if err != nil {
		t.Fatal(err)
	}
	var users []struct {
		Username string  `json:"username"`
		Email    *string `json:"email"`
		Shares   []struct {
			Pending                bool   `json:"pending"`
			ShareID                int    `json:"share_id"`
			ServerClientIdentifier string `json:"server_client_identifier"`
			Grants                 []struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
			} `json:"grants"`
		} `json:"shares"`
	}
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(users) != 1 || users[0].Username != "friend" || users[0].Email == nil || *users[0].Email != "friend@example.com" {
		t.Fatalf("users=%+v, want only external friend with returned email", users)
	}
	if len(users[0].Shares) != 1 {
		t.Fatalf("shares=%+v, want one nested share", users[0].Shares)
	}
	share := users[0].Shares[0]
	if !share.Pending || share.ShareID != 99 || share.ServerClientIdentifier != "owned-server" || len(share.Grants) != 1 || share.Grants[0].ID != 7 || share.Grants[0].Title != "Movies" {
		t.Fatalf("share=%+v, want pending nested share state, share ID, server identity, and grants", share)
	}
	if strings.Contains(out, "invite_status") {
		t.Fatalf("users JSON fabricated generic invite status: %s", out)
	}
}

func TestSharingLibrariesUsesExactOwnedResourceResolution(t *testing.T) {
	server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="owned-server" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/servers/owned-server" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" shared="0" title="Movies" type="movie"/></Server></MediaContainer>`))
	})
	configureSharingTestAccount(t, "alice", "owned-server", "owned-server", "account-token")
	useSharingPlexServer(t, server)

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "libraries", "--server", "owned-server", "--json") })
	if err != nil {
		t.Fatal(err)
	}
	var libraries []struct {
		ID    int    `json:"id"`
		Key   int    `json:"key"`
		Title string `json:"title"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal([]byte(out), &libraries); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(libraries) != 1 || libraries[0].ID != 7 || libraries[0].Key != 1 || libraries[0].Title != "Movies" || libraries[0].Type != "movie" {
		t.Fatalf("libraries=%+v, want exact owned-server sections", libraries)
	}
}

func TestSharingLibrariesRejectsUnsafeResourceBeforeLibraryEndpoint(t *testing.T) {
	cases := []struct {
		name      string
		resources string
		want      string
	}{
		{name: "no matching server", resources: `<MediaContainer><Device name="Other" clientIdentifier="other" provides="server" owned="1"/></MediaContainer>`, want: "no Plex server matches"},
		{name: "ambiguous server name", resources: `<MediaContainer><Device name="Owned Server" clientIdentifier="one" provides="server" owned="1"/><Device name="Owned Server" clientIdentifier="two" provides="server" owned="1"/></MediaContainer>`, want: "ambiguous Plex server"},
		{name: "non-owned server", resources: `<MediaContainer><Device name="Owned Server" clientIdentifier="owned-server" provides="server" owned="0"/></MediaContainer>`, want: "not owned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			libraryReached := false
			server := sharingTestServer(t, tc.resources, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/api/servers/") {
					libraryReached = true
				}
				http.NotFound(w, r)
			})
			configureSharingTestAccount(t, "alice", "Owned Server", "", "account-token")
			useSharingPlexServer(t, server)

			_, err := run(t, "sharing", "libraries", "--server", "Owned Server")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
			if libraryReached {
				t.Fatal("library endpoint was reached before exact owned-resource resolution")
			}
		})
	}
}

func TestSharingInvitePreflightAndMutationSafety(t *testing.T) {
	t.Run("invalid selections make no HTTP request", func(t *testing.T) {
		requests := 0
		server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.NotFound(w, r)
		})
		configureSharingTestAccount(t, "alice", "owned-server", "owned-server", "account-token")
		useSharingPlexServer(t, server)

		for _, args := range [][]string{
			{"sharing", "invite", "friend@example.com", "--server", "owned-server"},
			{"sharing", "invite", "friend@example.com", "--server", "owned-server", "--libraries", ""},
			{"sharing", "invite", "friend@example.com", "--server", "owned-server", "--libraries", "7", "--all-libraries"},
		} {
			if _, err := run(t, args...); err == nil {
				t.Fatalf("%v: expected preflight error", args)
			}
		}
		if requests != 0 {
			t.Fatalf("requests=%d, want no request for invalid selections", requests)
		}
	})

	t.Run("dry run reports target without HTTP", func(t *testing.T) {
		requests := 0
		server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.NotFound(w, r)
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		var err error
		out := captureStdout(t, func() {
			_, err = run(t, "sharing", "invite", "friend@example.com", "--server", "owned-server", "--libraries", "7,8", "--dry-run")
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "friend@example.com") || !strings.Contains(out, "owned-server") || !strings.Contains(out, "7,8") {
			t.Fatalf("dry-run output=%q, want target, server, and grants", out)
		}
		if requests != 0 {
			t.Fatalf("requests=%d, want dry-run to make no request", requests)
		}
	})

	t.Run("all libraries resolves global Plex IDs before one invite", func(t *testing.T) {
		requests := 0
		var invite map[string]any
		server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="machine-1" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			switch r.URL.Path {
			case "/api/servers/machine-1":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/><Section id="8" key="2" title="TV"/></Server></MediaContainer>`))
			case "/api/servers/machine-1/shared_servers":
				if r.Method != http.MethodPost {
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&invite); err != nil {
					t.Fatal(err)
				}
				w.WriteHeader(http.StatusCreated)
			default:
				http.NotFound(w, r)
			}
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		if _, err := run(t, "sharing", "invite", "friend@example.com", "--server", "owned-server", "--all-libraries"); err != nil {
			t.Fatal(err)
		}
		if requests != 2 { // ServerLibraries then the sole mutation; resource discovery is intercepted separately.
			t.Fatalf("requests=%d, want library discovery plus one POST", requests)
		}
		shared, ok := invite["shared_server"].(map[string]any)
		if !ok || !reflect.DeepEqual(shared["library_section_ids"], []any{float64(7), float64(8)}) || shared["invited_email"] != "friend@example.com" {
			t.Fatalf("invite=%#v, want all global sections and target", invite)
		}
	})

	t.Run("unknown global library makes no mutation", func(t *testing.T) {
		postReached := false
		server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="machine-1" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/servers/machine-1":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/></Server></MediaContainer>`))
			case "/api/servers/machine-1/shared_servers":
				postReached = true
				http.Error(w, "unexpected mutation", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		if _, err := run(t, "sharing", "invite", "friend@example.com", "--server", "owned-server", "--libraries", "99"); err == nil || !strings.Contains(err.Error(), "unknown Plex.tv library ID") {
			t.Fatalf("error=%v, want unknown global library error", err)
		}
		if postReached {
			t.Fatal("unknown global library sent a mutation")
		}
	})
}

func TestSharingUpdatePreflightAndReplacementSafety(t *testing.T) {
	t.Run("requires explicit server selector", func(t *testing.T) {
		requests := 0
		server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.NotFound(w, r)
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		if _, err := run(t, "sharing", "update", "99", "--libraries", "7"); err == nil || !strings.Contains(err.Error(), "requires --server") {
			t.Fatalf("error=%v, want explicit --server requirement", err)
		}
		if requests != 0 {
			t.Fatalf("requests=%d, want selector preflight to make no requests", requests)
		}
	})

	t.Run("dry run names replacement target without HTTP", func(t *testing.T) {
		requests := 0
		server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.NotFound(w, r)
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		var err error
		out := captureStdout(t, func() {
			_, err = run(t, "sharing", "update", "99", "--server", "owned-server", "--libraries", "7,8", "--dry-run")
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"REPLACE", "99", "owned-server", "machine-1", "7,8"} {
			if !strings.Contains(out, want) {
				t.Fatalf("dry-run output=%q, want %q", out, want)
			}
		}
		if requests != 0 {
			t.Fatalf("requests=%d, want dry-run to make no requests", requests)
		}
	})

	t.Run("all libraries replaces grants with global IDs", func(t *testing.T) {
		putReached := false
		server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="machine-1" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/servers/machine-1":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/><Section id="8" key="2" title="TV"/></Server></MediaContainer>`))
			case "/api/servers/machine-1/shared_servers/99":
				putReached = true
				if r.Method != http.MethodPut {
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
					return
				}
				var update map[string]any
				if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"server_id": "machine-1", "shared_server": map[string]any{"library_section_ids": []any{float64(7), float64(8)}}}
				if !reflect.DeepEqual(update, want) {
					t.Errorf("update=%#v, want %#v", update, want)
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		if _, err := run(t, "sharing", "update", "99", "--server", "owned-server", "--all-libraries"); err != nil {
			t.Fatal(err)
		}
		if !putReached {
			t.Fatal("expected exactly one replacement PUT")
		}
	})

	t.Run("invalid share and unknown library send no PUT", func(t *testing.T) {
		putReached := false
		server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="machine-1" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/servers/machine-1":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/></Server></MediaContainer>`))
			case "/api/servers/machine-1/shared_servers/99":
				putReached = true
				http.Error(w, "unexpected mutation", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		})
		configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
		useSharingPlexServer(t, server)

		for _, args := range [][]string{
			{"sharing", "update", "0", "--server", "owned-server", "--libraries", "7"},
			{"sharing", "update", "99", "--server", "owned-server", "--libraries", "99"},
		} {
			if _, err := run(t, args...); err == nil {
				t.Fatalf("%v: expected preflight error", args)
			}
		}
		if putReached {
			t.Fatal("invalid or unknown library update sent a PUT")
		}
	})
}

func TestSharingRemoveRequiresConfirmationAndDryRunMakesNoRequest(t *testing.T) {
	requests := 0
	server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
	useSharingPlexServer(t, server)

	if _, err := run(t, "sharing", "remove", "99", "--server", "owned-server"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error=%v, want explicit --yes confirmation requirement", err)
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want no request without --yes", requests)
	}

	var err error
	out := captureStdout(t, func() {
		_, err = run(t, "sharing", "remove", "99", "--server", "owned-server", "--yes", "--dry-run")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"would revoke", "99", "owned-server", "machine-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output=%q, want %q", out, want)
		}
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want dry-run to make no request", requests)
	}
}

func TestSharingRemoveUsesFreshOwnedResourceAndExactBodylessDelete(t *testing.T) {
	deleteReached := false
	server := sharingTestServer(t, `<MediaContainer><Device name="Resolved Server" clientIdentifier="resolved-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/servers/resolved-machine/shared_servers/99" {
			http.NotFound(w, r)
			return
		}
		deleteReached = true
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("DELETE body=%q, want no body", body)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	configureSharingTestAccount(t, "alice", "configured-server", "resolved-machine", "account-token")
	useSharingPlexServer(t, server)

	var err error
	out := captureStdout(t, func() {
		_, err = run(t, "sharing", "remove", "99", "--server", "configured-server", "--yes")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleteReached {
		t.Fatal("expected DELETE after fresh exact owned-resource resolution")
	}
	for _, want := range []string{"REVOKED", "99", "Resolved Server", "resolved-machine"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output=%q, want %q", out, want)
		}
	}
	if strings.Contains(out, "account-token") {
		t.Fatalf("output leaked Plex token: %q", out)
	}
}

func TestSharingRemoveRejectsInvalidInputWithoutRequest(t *testing.T) {
	requests := 0
	server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	})
	configureSharingTestAccount(t, "alice", "owned-server", "machine-1", "account-token")
	useSharingPlexServer(t, server)

	for _, args := range [][]string{
		{"sharing", "remove", "0", "--server", "owned-server", "--yes"},
		{"sharing", "remove", "99", "--yes"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Fatalf("%v: expected validation error", args)
		}
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want no request for invalid removal", requests)
	}
}

func TestSharingCommandsAreReadOnly(t *testing.T) {
	root := NewRoot()
	for _, path := range [][]string{{"sharing", "users"}, {"sharing", "libraries"}} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd.Name() != path[len(path)-1] {
			t.Fatalf("%s registration: cmd=%v err=%v", strings.Join(path, " "), cmd, err)
		}
		if cmd.Flags().Lookup("token") != nil {
			t.Fatalf("%s must not expose a token flag", strings.Join(path, " "))
		}
	}
}

func sharingTestServer(t *testing.T, resources string, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(resources))
			return
		}
		handler(w, r)
	}))
}

func configureSharingTestAccount(t *testing.T, account, serverID, machineID, token string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PLEXCTL_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("PLEXCTL_TOKENS_FILE", filepath.Join(dir, "tokens.json"))
	cfg := config.Config{CurrentAccount: account, Accounts: map[string]config.Account{account: {Username: account, TokenKey: "account/" + account}}, ServersV2: map[string]config.ServerProfile{}}
	if serverID != "" {
		cfg.CurrentServer = serverID
		cfg.ServersV2[serverID] = config.ServerProfile{Account: account, Name: serverID, MachineIdentifier: machineID, TokenKey: "account/" + account}
	}
	if err := config.Save(config.Path(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEXCTL_TOKEN_ACCOUNT_"+strings.ToUpper(account), token)
}

func useSharingPlexServer(t *testing.T, server *httptest.Server) {
	t.Helper()
	old := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { return plexauth.New(server.URL, "plexctl", server.Client()) }
	t.Cleanup(func() { sharingPlexClient = old })
}
