package plexauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedUsersSharing(t *testing.T) {
	token := "sharing-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/users/" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/xml")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><User id="42" username="friend" email="friend@example.com" home="0"><Server id="99" serverId="123" machineIdentifier="machine-1" name="Plex" allLibraries="0" pending="1" owned="1"/></User></MediaContainer>`))
	}))
	defer server.Close()

	users, err := New(server.URL, "plexctl-test", server.Client()).SharedUsers(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("got %d users, want 1", len(users))
	}
	user := users[0]
	if user.ID != 42 || user.Username != "friend" || user.Email == nil || *user.Email != "friend@example.com" || user.Home {
		t.Fatalf("unexpected shared user: %+v", user)
	}
	if len(user.ServerShares) != 1 {
		t.Fatalf("shares=%+v, want one", user.ServerShares)
	}
	share := user.ServerShares[0]
	if share.ID != 99 || share.ServerID != 123 || share.MachineIdentifier != "machine-1" || share.Name != "Plex" || share.AllLibraries || !share.Pending || !share.Owned {
		t.Fatalf("unexpected server share: %+v", share)
	}
}

func TestSharedServerSectionsSharing(t *testing.T) {
	token := "sharing-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/servers/machine-1/shared_servers/99" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/xml")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<SharedServer><Section id="7" key="1" shared="1" title="Movies" type="movie"/></SharedServer>`))
	}))
	defer server.Close()

	sections, err := New(server.URL, "plexctl-test", server.Client()).SharedServerSections(context.Background(), token, "machine-1", 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	section := sections[0]
	if section.ID != 7 || section.Key != 1 || !section.Shared || section.Title != "Movies" || section.Type != "movie" {
		t.Fatalf("unexpected section: %+v", section)
	}
}

func TestSharedUsersSharingReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer server.Close()

	users, err := New(server.URL, "plexctl-test", server.Client()).SharedUsers(context.Background(), "sharing-token")
	if users != nil {
		t.Fatalf("users=%+v, want nil", users)
	}
	var statusErr *HTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden || statusErr.Path != "/api/users/" {
		t.Fatalf("error=%v, want HTTP status error for %d", err, http.StatusForbidden)
	}
}

func TestSharedUsersSharingRejectsMalformedAndOversizedResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `<MediaContainer><User>`},
		{name: "unexpected root", body: `<NotUsers/>`},
		{name: "oversized", body: strings.Repeat("x", (4<<20)+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			users, err := New(server.URL, "plexctl-test", server.Client()).SharedUsers(context.Background(), "sharing-token")
			if err == nil {
				t.Fatal("expected response error")
			}
			if users != nil {
				t.Fatalf("users=%+v, want nil", users)
			}
		})
	}
}

func assertPlexHeaders(t *testing.T, r *http.Request, token, accept string) {
	t.Helper()
	if got := r.Header.Get("Accept"); got != accept {
		t.Errorf("Accept=%q, want %q", got, accept)
	}
	if got := r.Header.Get("X-Plex-Client-Identifier"); got != "plexctl-test" {
		t.Errorf("X-Plex-Client-Identifier=%q, want plexctl-test", got)
	}
	if got := r.Header.Get("X-Plex-Product"); got != "plexctl" {
		t.Errorf("X-Plex-Product=%q, want plexctl", got)
	}
	if got := r.Header.Get("X-Plex-Token"); got != token {
		t.Errorf("X-Plex-Token=%q, want provided token", got)
	}
}
