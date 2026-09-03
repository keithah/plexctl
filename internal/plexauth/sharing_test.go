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
		if r.Method != http.MethodGet || r.URL.Path != "/api/users" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":42,"username":"friend","email":"friend@example.com","invited":true,"friend":false}]`))
	}))
	defer server.Close()

	users, err := New(server.URL, "plexctl-test", server.Client()).SharedUsers(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("got %d users, want 1", len(users))
	}
	if users[0].ID != 42 || users[0].Username != "friend" || users[0].Email == nil || *users[0].Email != "friend@example.com" || !users[0].Invited || users[0].Friend {
		t.Fatalf("unexpected shared user: %+v", users[0])
	}
}

func TestSharedServersSharing(t *testing.T) {
	token := "sharing-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/servers/machine-1/shared_servers" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":99,"userID":42,"librarySectionIDs":[1,2],"allLibraries":false,"allowSync":true,"allowDownloads":false}]`))
	}))
	defer server.Close()

	shares, err := New(server.URL, "plexctl-test", server.Client()).SharedServers(context.Background(), token, "machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("got %d shares, want 1", len(shares))
	}
	share := shares[0]
	if share.ID != 99 || share.UserID != 42 || len(share.LibrarySectionIDs) != 2 || share.LibrarySectionIDs[0] != 1 || share.LibrarySectionIDs[1] != 2 {
		t.Fatalf("unexpected shared server: %+v", share)
	}
	if share.AllLibraries || !share.AllowSync || share.AllowDownloads {
		t.Fatalf("unexpected share settings: %+v", share)
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
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusForbidden {
		t.Fatalf("error=%v, want HTTP status error for %d", err, http.StatusForbidden)
	}
}

func TestSharedUsersSharingRejectsMalformedAndOversizedResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `[{`},
		{name: "oversized", body: strings.Repeat("x", (2<<20)+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
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

func assertPlexHeaders(t *testing.T, r *http.Request, token string) {
	t.Helper()
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept=%q, want application/json", got)
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
