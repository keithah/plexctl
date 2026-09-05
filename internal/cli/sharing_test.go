package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/plexauth"
	"github.com/keithah/plexctl/internal/sharinghistory"
)

func TestSharingUsersJSONShowsNestedShareStateAndGrants(t *testing.T) {
	server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="owned-server" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User username="home-user" home="1"><Server id="1" serverId="1" machineIdentifier="owned-server" name="Owned Server"/></User><User username="friend" email="friend@example.com"><Server id="97" serverId="121" machineIdentifier="stale-server" name="Stale Server" allLibraries="0" pending="0" owned="1"/><Server id="98" serverId="122" machineIdentifier="foreign-server" name="Foreign Server" allLibraries="0" pending="0" owned="0"/><Server id="99" serverId="123" machineIdentifier="owned-server" name="Owned Server" allLibraries="0" pending="1" owned="1"/></User></MediaContainer>`))
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

func TestSharingUnprofiledSelectorsUseCurrentAccountAndFreshOwnedResource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		operation string
		selector  string
	}{
		{name: "libraries accepts exact fresh name", operation: "libraries", selector: "Fresh Server"},
		{name: "libraries accepts exact fresh client identifier", operation: "libraries", selector: "fresh-machine"},
		{name: "invite accepts exact fresh name", operation: "invite", selector: "Fresh Server"},
		{name: "update accepts exact fresh client identifier", operation: "update", selector: "fresh-machine"},
		{name: "remove accepts exact fresh name", operation: "remove", selector: "Fresh Server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutationReached := false
			server := sharingTestServer(t, `<MediaContainer><Device name="Fresh Server" clientIdentifier="fresh-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/users/":
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<MediaContainer><User username="friend" home="0"><Server id="99" machineIdentifier="fresh-machine" owned="1"/></User></MediaContainer>`))
				case "/api/servers/fresh-machine":
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/></Server></MediaContainer>`))
				case "/api/servers/fresh-machine/shared_servers":
					if tc.operation != "invite" || r.Method != http.MethodPost {
						http.NotFound(w, r)
						return
					}
					mutationReached = true
					w.WriteHeader(http.StatusCreated)
				case "/api/servers/fresh-machine/shared_servers/99":
					if tc.operation == "remove" && r.Method == http.MethodGet {
						w.Header().Set("Content-Type", "application/xml")
						_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" title="Movies"/></SharedServer>`))
						return
					}
					wantMethod := http.MethodPut
					if tc.operation == "remove" {
						wantMethod = http.MethodDelete
					}
					if r.Method != wantMethod {
						http.NotFound(w, r)
						return
					}
					mutationReached = true
					w.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(w, r)
				}
			})
			configureSharingTestAccount(t, "alice", "configured-server", "configured-machine", "account-token")
			useSharingPlexServer(t, server)

			args := []string{"sharing", tc.operation}
			switch tc.operation {
			case "invite":
				args = append(args, "friend@example.com")
			case "update", "remove":
				args = append(args, "99")
			}
			args = append(args, "--server", tc.selector)
			switch tc.operation {
			case "invite", "update":
				args = append(args, "--libraries", "7")
			case "remove":
				args = append(args, "--yes")
			}

			var err error
			out := captureStdout(t, func() { _, err = run(t, args...) })
			if err != nil {
				t.Fatal(err)
			}
			if tc.operation != "libraries" && !mutationReached {
				t.Fatalf("%s did not reach its exact mutation endpoint", tc.operation)
			}
			if strings.Contains(out, "account-token") {
				t.Fatalf("output leaked Plex token: %q", out)
			}
		})
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

func TestSharingMutationsRequireFreshExactExternalShare(t *testing.T) {
	for _, operation := range []string{"update", "remove"} {
		t.Run(operation, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				shareID    string
				wantMutate bool
			}{
				{name: "Home user share", shareID: "101"},
				{name: "foreign non-owned share", shareID: "102"},
				{name: "stale mismatched server share", shareID: "103"},
				{name: "valid external owned share", shareID: "104", wantMutate: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					mutations := 0
					usersRequested := false
					server := sharingTestServer(t, `<MediaContainer><Device name="Resolved Server" clientIdentifier="resolved-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/api/users/":
							usersRequested = true
							w.Header().Set("Content-Type", "application/xml")
							_, _ = w.Write([]byte(`<MediaContainer><User username="home" home="1"><Server id="101" machineIdentifier="resolved-machine" owned="1"/></User><User username="foreign" home="0"><Server id="102" machineIdentifier="resolved-machine" owned="0"/><Server id="103" machineIdentifier="other-machine" owned="1"/><Server id="104" machineIdentifier="resolved-machine" owned="1"/></User></MediaContainer>`))
						case "/api/servers/resolved-machine":
							w.Header().Set("Content-Type", "application/xml")
							_, _ = w.Write([]byte(`<MediaContainer><Server><Section id="7" key="1" title="Movies"/></Server></MediaContainer>`))
						case "/api/servers/resolved-machine/shared_servers/" + tc.shareID:
							if operation == "remove" && r.Method == http.MethodGet {
								w.Header().Set("Content-Type", "application/xml")
								_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" title="Movies"/></SharedServer>`))
								return
							}
							mutations++
							if !tc.wantMutate {
								t.Errorf("unsafe %s for share %s", r.Method, tc.shareID)
							}
							if operation == "update" && r.Method != http.MethodPut {
								t.Errorf("method=%s, want PUT", r.Method)
							}
							if operation == "remove" && r.Method != http.MethodDelete {
								t.Errorf("method=%s, want DELETE", r.Method)
							}
							w.WriteHeader(http.StatusNoContent)
						default:
							http.NotFound(w, r)
						}
					})
					configureSharingTestAccount(t, "alice", "configured-server", "resolved-machine", "account-token")
					useSharingPlexServer(t, server)

					args := []string{"sharing", operation, tc.shareID, "--server", "configured-server"}
					if operation == "update" {
						args = append(args, "--libraries", "7")
					} else {
						args = append(args, "--yes")
					}
					_, err := run(t, args...)
					if tc.wantMutate {
						if err != nil {
							t.Fatal(err)
						}
						if mutations != 1 {
							t.Fatalf("mutations=%d, want one %s", mutations, operation)
						}
					} else {
						if err == nil || !strings.Contains(err.Error(), "external") {
							t.Fatalf("error=%v, want external-share authorization failure", err)
						}
						if strings.Contains(err.Error(), "account-token") {
							t.Fatalf("error leaked Plex token: %q", err)
						}
						if mutations != 0 {
							t.Fatalf("mutations=%d, want zero", mutations)
						}
					}
					if !usersRequested {
						t.Fatal("fresh shared-user list was not requested")
					}
				})
			}
		})
	}
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
			case "/api/users/":
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<MediaContainer><User username="friend" home="0"><Server id="99" machineIdentifier="machine-1" owned="1"/></User></MediaContainer>`))
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

	if _, err := run(t, "sharing", "remove", "99", "--server", "owned-server", "--dry-run"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error=%v, want explicit --yes confirmation even for dry-run", err)
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want dry-run without --yes to make no request", requests)
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

func TestSharingRemoveRecordsExternalShareOnlyAfterExactDelete(t *testing.T) {
	token := "account-token"
	deletes := 0
	server := sharingTestServer(t, `<MediaContainer><Device name="Fresh Server" clientIdentifier="fresh-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User id="42" username="friend" email="friend@example.com" home="0"><Server id="99" serverId="123" machineIdentifier="fresh-machine" name="Stale Server" allLibraries="1" pending="1" owned="1"/></User></MediaContainer>`))
		case "/api/servers/fresh-machine/shared_servers/99":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" shared="1" title="Movies" type="movie"/><Section id="8" key="2" shared="1" title="TV" type="show"/></SharedServer>`))
				return
			}
			if r.Method != http.MethodDelete {
				http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if len(body) != 0 {
				t.Fatalf("DELETE body=%q, want no body", body)
			}
			deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	})
	configureSharingTestAccount(t, "alice", "configured-server", "fresh-machine", token)
	historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
	useSharingPlexServer(t, server)

	var err error
	out := captureStdout(t, func() {
		_, err = run(t, "sharing", "remove", "99", "--server", "configured-server", "--yes")
	})
	if err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("DELETE requests=%d, want exactly one", deletes)
	}
	records, err := sharinghistory.Open(historyPath).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%+v, want one removal record", records)
	}
	record := records[0]
	if record.PlexUserID != 42 || record.Username != "friend" || record.Email == nil || *record.Email != "friend@example.com" || record.ShareID != 99 || record.ServerName != "Fresh Server" || record.ServerClientIdentifier != "fresh-machine" || !record.AllLibraries || !record.Pending || !reflect.DeepEqual(record.LibrarySectionIDs, []int{7, 8}) {
		t.Fatalf("record=%+v, want complete external-share snapshot with fresh server details", record)
	}
	if strings.Contains(out, token) || strings.Contains(fmt.Sprintf("%+v", record), token) {
		t.Fatalf("output or history leaked Plex token: output=%q record=%+v", out, record)
	}
}

func TestSharingRemoveDoesNotRecordWithoutProvenSuccessfulRevocation(t *testing.T) {
	t.Run("dry run and invalid input make no request or history record", func(t *testing.T) {
		requests := 0
		server := sharingTestServer(t, `<MediaContainer/>`, func(w http.ResponseWriter, r *http.Request) {
			requests++
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		})
		configureSharingTestAccount(t, "alice", "configured-server", "machine-1", "account-token")
		historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
		t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
		useSharingPlexServer(t, server)

		if _, err := run(t, "sharing", "remove", "99", "--server", "configured-server", "--yes", "--dry-run"); err != nil {
			t.Fatalf("dry-run error=%v", err)
		}
		for _, args := range [][]string{
			{"sharing", "remove", "0", "--server", "configured-server", "--yes"},
			{"sharing", "remove", "99", "--server", "configured-server"},
		} {
			if _, err := run(t, args...); err == nil {
				t.Fatalf("%v: expected validation or confirmation error", args)
			}
		}
		if requests != 0 {
			t.Fatalf("requests=%d, want zero", requests)
		}
		records, err := sharinghistory.Open(historyPath).List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 0 {
			t.Fatalf("records=%+v, want none", records)
		}
	})

	t.Run("Home foreign and ambiguous shares make no grant request DELETE or history record", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			shareID string
		}{
			{name: "Home", shareID: "101"},
			{name: "foreign", shareID: "102"},
			{name: "ambiguous", shareID: "103"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				grantOrDelete := 0
				server := sharingTestServer(t, `<MediaContainer><Device name="Fresh Server" clientIdentifier="fresh-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/users/":
						w.Header().Set("Content-Type", "application/xml")
						_, _ = w.Write([]byte(`<MediaContainer><User username="home" home="1"><Server id="101" machineIdentifier="fresh-machine" owned="1"/></User><User username="foreign" home="0"><Server id="102" machineIdentifier="fresh-machine" owned="0"/><Server id="103" machineIdentifier="fresh-machine" owned="1"/></User><User username="another" home="0"><Server id="103" machineIdentifier="fresh-machine" owned="1"/></User></MediaContainer>`))
					default:
						grantOrDelete++
						http.Error(w, "unsafe request", http.StatusInternalServerError)
					}
				})
				configureSharingTestAccount(t, "alice", "configured-server", "fresh-machine", "account-token")
				historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
				t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
				useSharingPlexServer(t, server)

				if _, err := run(t, "sharing", "remove", tc.shareID, "--server", "configured-server", "--yes"); err == nil || !strings.Contains(err.Error(), "external") {
					t.Fatalf("error=%v, want external-share validation failure", err)
				}
				if grantOrDelete != 0 {
					t.Fatalf("grant or DELETE requests=%d, want zero", grantOrDelete)
				}
				records, err := sharinghistory.Open(historyPath).List(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(records) != 0 {
					t.Fatalf("records=%+v, want none", records)
				}
			})
		}
	})

	t.Run("grant fetch and DELETE failures leave no history record", func(t *testing.T) {
		for _, tc := range []struct {
			name         string
			grantStatus  int
			deleteStatus int
		}{
			{name: "grant fetch", grantStatus: http.StatusInternalServerError},
			{name: "DELETE", deleteStatus: http.StatusBadGateway},
		} {
			t.Run(tc.name, func(t *testing.T) {
				deletes := 0
				server := sharingTestServer(t, `<MediaContainer><Device name="Fresh Server" clientIdentifier="fresh-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/api/users/":
						w.Header().Set("Content-Type", "application/xml")
						_, _ = w.Write([]byte(`<MediaContainer><User id="42" username="friend" home="0"><Server id="99" machineIdentifier="fresh-machine" owned="1"/></User></MediaContainer>`))
					case "/api/servers/fresh-machine/shared_servers/99":
						if r.Method == http.MethodGet {
							if tc.grantStatus != 0 {
								http.Error(w, "grant failure", tc.grantStatus)
								return
							}
							w.Header().Set("Content-Type", "application/xml")
							_, _ = w.Write([]byte(`<SharedServer><Section id="7"/></SharedServer>`))
							return
						}
						if r.Method == http.MethodDelete {
							deletes++
							http.Error(w, "DELETE failure", tc.deleteStatus)
							return
						}
						http.NotFound(w, r)
					default:
						http.NotFound(w, r)
					}
				})
				configureSharingTestAccount(t, "alice", "configured-server", "fresh-machine", "account-token")
				historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
				t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
				useSharingPlexServer(t, server)

				if _, err := run(t, "sharing", "remove", "99", "--server", "configured-server", "--yes"); err == nil {
					t.Fatal("expected grant or DELETE failure")
				}
				if tc.grantStatus != 0 && deletes != 0 {
					t.Fatalf("DELETE requests=%d, want zero after grant failure", deletes)
				}
				if tc.deleteStatus != 0 && deletes != 1 {
					t.Fatalf("DELETE requests=%d, want one", deletes)
				}
				records, err := sharinghistory.Open(historyPath).List(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(records) != 0 {
					t.Fatalf("records=%+v, want none", records)
				}
			})
		}
	})
}

func TestSharingRemoveReportsPartialSuccessWhenHistoryAppendFails(t *testing.T) {
	deletes := 0
	server := sharingTestServer(t, `<MediaContainer><Device name="Fresh Server" clientIdentifier="fresh-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User id="42" username="friend" home="0"><Server id="99" machineIdentifier="fresh-machine" owned="1"/></User></MediaContainer>`))
		case "/api/servers/fresh-machine/shared_servers/99":
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(`<SharedServer><Section id="7"/></SharedServer>`))
				return
			}
			if r.Method == http.MethodDelete {
				deletes++
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	configureSharingTestAccount(t, "alice", "configured-server", "fresh-machine", "account-token")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", t.TempDir()) // A directory cannot be opened as the SQLite history file.
	useSharingPlexServer(t, server)

	var err error
	out := captureStdout(t, func() {
		_, err = run(t, "sharing", "remove", "99", "--server", "configured-server", "--yes")
	})
	if err == nil || !strings.Contains(err.Error(), "revocation succeeded but local history recording failed") {
		t.Fatalf("error=%v, want explicit partial-success error", err)
	}
	if deletes != 1 {
		t.Fatalf("DELETE requests=%d, want exactly one without retry", deletes)
	}
	if strings.Contains(out, "REVOKED") {
		t.Fatalf("output=%q, must not report success when local history append fails", out)
	}
}

func TestSharingRemoveUsesFreshOwnedResourceAndExactBodylessDelete(t *testing.T) {
	deleteReached := false
	server := sharingTestServer(t, `<MediaContainer><Device name="Resolved Server" clientIdentifier="resolved-machine" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/users/" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User username="friend" home="0"><Server id="99" machineIdentifier="resolved-machine" owned="1"/></User></MediaContainer>`))
			return
		}
		if r.URL.Path == "/api/servers/resolved-machine/shared_servers/99" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" title="Movies"/></SharedServer>`))
			return
		}
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

func TestSharingRemovedListsLocalHistoryNewestFirstWithoutPlexOrAuth(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
	t.Setenv("PLEXCTL_CONFIG", filepath.Join(t.TempDir(), "missing-config.json"))
	t.Setenv("PLEXCTL_TOKENS_FILE", filepath.Join(t.TempDir(), "missing-tokens.json"))
	t.Setenv("PLEXCTL_TOKEN_ACCOUNT_ALICE", "poisoned-token")
	history := sharinghistory.Open(historyPath)
	olderEmail := "older@example.com"
	newerEmail := "newer@example.com"
	for _, record := range []sharinghistory.Record{
		{RemovedAt: time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC), PlexUserID: 1, Username: "older", Email: &olderEmail, ShareID: 101, ServerName: "Older Server", ServerClientIdentifier: "older-server", AllLibraries: true, LibrarySectionIDs: []int{9, 3}},
		{RemovedAt: time.Date(2026, time.September, 4, 21, 0, 0, 0, time.UTC), PlexUserID: 2, Username: "newer", Email: &newerEmail, ShareID: 202, ServerName: "Newer Server", ServerClientIdentifier: "newer-server", Pending: true, LibrarySectionIDs: []int{8, 2}},
	} {
		if err := history.Append(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "removed") })
	if err != nil {
		t.Fatal(err)
	}
	want := "2026-09-04T21:00:00Z	newer	newer@example.com	share_id=202	server=Newer Server	newer-server	all_libraries=false	pending=true	grants=2,8\n" +
		"2026-09-04T20:00:00Z	older	older@example.com	share_id=101	server=Older Server	older-server	all_libraries=true	pending=false	grants=3,9\n"
	if out != want {
		t.Fatalf("table output=%q, want %q", out, want)
	}
	if strings.Contains(out, "poisoned-token") {
		t.Fatalf("output leaked token: %q", out)
	}
}

func TestSharingRemovedJSONUsesStableFieldsAndNewestFirst(t *testing.T) {
	historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
	history := sharinghistory.Open(historyPath)
	for _, record := range []sharinghistory.Record{
		{RemovedAt: time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC), PlexUserID: 1, Username: "older", ShareID: 101, ServerName: "Older Server", ServerClientIdentifier: "older-server", AllLibraries: true, LibrarySectionIDs: []int{9, 3}},
		{RemovedAt: time.Date(2026, time.September, 4, 21, 0, 0, 0, time.UTC), PlexUserID: 2, Username: "newer", ShareID: 202, ServerName: "Newer Server", ServerClientIdentifier: "newer-server", Pending: true, LibrarySectionIDs: []int{8, 2}},
	} {
		if err := history.Append(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "removed", "--json") })
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		RemovedAt              time.Time `json:"removed_at"`
		PlexUserID             int64     `json:"plex_user_id"`
		Username               string    `json:"username"`
		ShareID                int64     `json:"share_id"`
		ServerName             string    `json:"server_name"`
		ServerClientIdentifier string    `json:"server_client_identifier"`
		AllLibraries           bool      `json:"all_libraries"`
		Pending                bool      `json:"pending"`
		LibrarySectionIDs      []int     `json:"library_section_ids"`
	}
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(records) != 2 || records[0].Username != "newer" || records[0].ShareID != 202 || !reflect.DeepEqual(records[0].LibrarySectionIDs, []int{2, 8}) || records[1].Username != "older" || records[1].ShareID != 101 || !reflect.DeepEqual(records[1].LibrarySectionIDs, []int{3, 9}) {
		t.Fatalf("records=%+v, want stable newest-first local history", records)
	}
	for _, field := range []string{"removed_at", "plex_user_id", "username", "share_id", "server_name", "server_client_identifier", "all_libraries", "pending", "library_section_ids"} {
		if !strings.Contains(out, fmt.Sprintf("\"%s\"", field)) {
			t.Fatalf("JSON=%s, missing stable field %q", out, field)
		}
	}
}

func TestSharingRemovedEmptyOrMissingHistorySucceedsWithoutPlexOrAuth(t *testing.T) {
	for _, name := range []string{"missing", "empty"} {
		t.Run(name, func(t *testing.T) {
			historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
			t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
			t.Setenv("PLEXCTL_CONFIG", filepath.Join(t.TempDir(), "missing-config.json"))
			if name == "empty" {
				if err := sharinghistory.Open(historyPath).Append(t.Context(), sharinghistory.Record{RemovedAt: time.Now(), PlexUserID: 1, Username: "temporary", ShareID: 1, ServerName: "server", ServerClientIdentifier: "server"}); err != nil {
					t.Fatal(err)
				}
				if _, err := sharinghistory.Open(historyPath).PurgeBefore(t.Context(), time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			oldClient := sharingPlexClient
			sharingPlexClient = func() *plexauth.Client { panic("sharing removed must not create a Plex client") }
			t.Cleanup(func() { sharingPlexClient = oldClient })

			var err error
			out := captureStdout(t, func() { _, err = run(t, "sharing", "removed", "--json") })
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(out) != "[]" {
				t.Fatalf("output=%q, want empty JSON list", out)
			}
		})
	}
}

func TestSharingRemovedPurgeRejectsMissingOrInvalidDurationBeforeLocalOrPlexAccess(t *testing.T) {
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed purge must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", filepath.Join(t.TempDir(), "missing", "sharing-history.db"))

	for _, args := range [][]string{
		{"sharing", "removed", "purge", "--yes"},
		{"sharing", "removed", "purge", "--older-than", "30d", "--yes"},
		{"sharing", "removed", "purge", "--older-than", "0s", "--yes"},
		{"sharing", "removed", "purge", "--older-than", "-1h", "--yes"},
	} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "--older-than") {
			t.Fatalf("%v: error=%v, want --older-than validation failure", args, err)
		}
	}
}

func TestSharingRemovedPurgeRequiresYesWithoutMutationOrPlex(t *testing.T) {
	historyPath := sharingRemovedPurgeHistory(t)
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed purge must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })

	if _, err := run(t, "sharing", "removed", "purge", "--older-than", "2h"); err == nil || !strings.Contains(err.Error(), "explicit --yes") {
		t.Fatalf("error=%v, want explicit confirmation failure", err)
	}
	if records, err := sharinghistory.Open(historyPath).List(t.Context()); err != nil || len(records) != 3 {
		t.Fatalf("records after rejected purge = %d, %v; want 3 unchanged", len(records), err)
	}
}

func TestSharingRemovedPurgeDryRunCountsWithoutMutationOrPlex(t *testing.T) {
	historyPath := sharingRemovedPurgeHistory(t)
	oldNow := sharingHistoryNow
	sharingHistoryNow = func() time.Time { return time.Date(2026, time.September, 4, 22, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { sharingHistoryNow = oldNow })
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed purge must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "removed", "purge", "--older-than", "2h", "--dry-run") })
	if err != nil {
		t.Fatal(err)
	}
	if out != "dry run: 1 locally recorded removed share would be purged\n" {
		t.Fatalf("output=%q, want exact local match count", out)
	}
	if records, err := sharinghistory.Open(historyPath).List(t.Context()); err != nil || len(records) != 3 {
		t.Fatalf("records after dry-run = %d, %v; want 3 unchanged", len(records), err)
	}
}

func TestSharingRemovedPurgeDeletesOnlyRecordsStrictlyBeforeCutoffWithoutPlex(t *testing.T) {
	historyPath := sharingRemovedPurgeHistory(t)
	oldNow := sharingHistoryNow
	sharingHistoryNow = func() time.Time { return time.Date(2026, time.September, 4, 22, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { sharingHistoryNow = oldNow })
	oldClient := sharingPlexClient
	sharingPlexClient = func() *plexauth.Client { panic("sharing removed purge must not create a Plex client") }
	t.Cleanup(func() { sharingPlexClient = oldClient })

	var err error
	out := captureStdout(t, func() { _, err = run(t, "sharing", "removed", "purge", "--older-than", "2h", "--yes") })
	if err != nil {
		t.Fatal(err)
	}
	if out != "purged 1 locally recorded removed share\n" {
		t.Fatalf("output=%q, want exact deleted count", out)
	}
	records, err := sharinghistory.Open(historyPath).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Username != "newer" || records[1].Username != "at-cutoff" {
		t.Fatalf("records=%+v, want only records at or after cutoff", records)
	}
}

func TestSharingRemovedPurgeDoesNotOfferJSON(t *testing.T) {
	out, err := run(t, "sharing", "removed", "purge", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--json") {
		t.Fatalf("purge help must not offer --json: %s", out)
	}
}

func sharingRemovedPurgeHistory(t *testing.T) string {
	t.Helper()
	historyPath := filepath.Join(t.TempDir(), "sharing-history.db")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", historyPath)
	history := sharinghistory.Open(historyPath)
	for _, record := range []sharinghistory.Record{
		{RemovedAt: time.Date(2026, time.September, 4, 19, 59, 59, 0, time.UTC), PlexUserID: 1, Username: "older", ShareID: 1, ServerName: "server", ServerClientIdentifier: "server"},
		{RemovedAt: time.Date(2026, time.September, 4, 20, 0, 0, 0, time.UTC), PlexUserID: 2, Username: "at-cutoff", ShareID: 2, ServerName: "server", ServerClientIdentifier: "server"},
		{RemovedAt: time.Date(2026, time.September, 4, 20, 0, 1, 0, time.UTC), PlexUserID: 3, Username: "newer", ShareID: 3, ServerName: "server", ServerClientIdentifier: "server"},
	} {
		if err := history.Append(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	return historyPath
}

func TestSharingCommandsAreReadOnly(t *testing.T) {
	root := NewRoot()
	for _, path := range [][]string{{"sharing", "users"}, {"sharing", "libraries"}, {"sharing", "removed"}} {
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
