package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
			_, _ = w.Write([]byte(`<MediaContainer><User username="home-user" home="1"><Server id="1" serverId="1" machineIdentifier="owned-server" name="Owned Server"/></User><User username="friend" email="friend@example.com"><Server id="99" serverId="123" machineIdentifier="owned-server" name="Owned Server" allLibraries="0" pending="1" owned="1"/></User></MediaContainer>`))
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
