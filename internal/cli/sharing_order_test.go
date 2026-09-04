package cli

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSharingUsersJSONIsSorted(t *testing.T) {
	server := sharingTestServer(t, `<MediaContainer><Device name="Owned Server" clientIdentifier="owned-server" provides="server" owned="1"/></MediaContainer>`, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/users/":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer><User username="zebra"/><User username="alpha"/></MediaContainer>`))
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
		Username string `json:"username"`
	}
	if err := json.Unmarshal([]byte(out), &users); err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "alpha" || users[1].Username != "zebra" {
		t.Fatalf("users=%+v, want stable username ordering", users)
	}
}
