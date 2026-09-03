package plexauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestResolveOwnedResourceSharing(t *testing.T) {
	resources := []Resource{
		{Name: "Living Room", ClientIdentifier: "owned-1", Owned: true},
		{Name: "Living Room", ClientIdentifier: "owned-2", Owned: true},
		{Name: "Guest Server", ClientIdentifier: "guest-1", Owned: false},
	}

	t.Run("exact client identifier resolves an owned resource", func(t *testing.T) {
		got, err := ResolveOwnedResource(resources, "owned-1")
		if err != nil {
			t.Fatal(err)
		}
		if got.ClientIdentifier != "owned-1" {
			t.Fatalf("resource=%+v, want owned-1", got)
		}
	})

	t.Run("exact unique name resolves an owned resource", func(t *testing.T) {
		got, err := ResolveOwnedResource(resources, "Guest Server")
		if err == nil {
			t.Fatalf("resource=%+v, want non-owned error", got)
		}
		if !strings.Contains(err.Error(), "not owned") || !strings.Contains(err.Error(), "guest-1") {
			t.Fatalf("error=%q, want non-owned resource details", err)
		}

		got, err = ResolveOwnedResource(append(resources, Resource{Name: "Office", ClientIdentifier: "owned-3", Owned: true}), "Office")
		if err != nil {
			t.Fatal(err)
		}
		if got.ClientIdentifier != "owned-3" {
			t.Fatalf("resource=%+v, want owned-3", got)
		}
	})

	t.Run("ambiguous same-name owned resources list safe candidates", func(t *testing.T) {
		_, err := ResolveOwnedResource(resources, "Living Room")
		if err == nil {
			t.Fatal("expected ambiguous resource error")
		}
		if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "owned-1") || !strings.Contains(err.Error(), "owned-2") {
			t.Fatalf("error=%q, want ambiguous candidate client identifiers", err)
		}
	})

	t.Run("matching non-owned client identifier is rejected", func(t *testing.T) {
		_, err := ResolveOwnedResource(resources, "guest-1")
		if err == nil {
			t.Fatal("expected non-owned resource error")
		}
		if !strings.Contains(err.Error(), "not owned") || !strings.Contains(err.Error(), "Guest Server") || !strings.Contains(err.Error(), "guest-1") {
			t.Fatalf("error=%q, want non-owned resource details", err)
		}
	})
}

func TestServerLibrariesSharing(t *testing.T) {
	token := "sharing-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/servers/machine-1" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/xml")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><Server machineIdentifier="machine-1" owned="1"><Section id="7" key="1" title="Movies" type="movie"/><Section id="8" key="2" title="TV" type="show"/></Server></MediaContainer>`))
	}))
	defer server.Close()

	sections, err := New(server.URL, "plexctl-test", server.Client()).ServerLibraries(context.Background(), token, "machine-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].ID != 7 || sections[0].Key != 1 || sections[0].Title != "Movies" || sections[1].ID != 8 {
		t.Fatalf("sections=%+v, want Plex.tv server sections", sections)
	}
}

func TestValidateLibraryIDsSharing(t *testing.T) {
	libraries := []LibrarySection{{ID: 7, Key: 1, Title: "Movies"}, {ID: 8, Key: 2, Title: "TV"}}

	if err := ValidateLibraryIDs(libraries, []string{"7", "8"}); err != nil {
		t.Fatalf("validate known library IDs: %v", err)
	}
	if err := ValidateLibraryIDs(libraries, []string{"1"}); err == nil {
		t.Fatal("expected local key to be rejected as an unknown Plex.tv library ID")
	} else if !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "1") {
		t.Fatalf("error=%q, want unknown requested ID", err)
	}
}

func TestSharingInviteSendsVerifiedTypedRequest(t *testing.T) {
	token := "sharing-token"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/api/servers/machine-1/shared_servers" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/json")
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type=%q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"server_id": "machine-1",
			"shared_server": map[string]any{
				"library_section_ids": []any{float64(7), float64(8)},
				"invited_email":       "friend@example.com",
			},
			"sharing_settings": map[string]any{
				"allowSync":         "0",
				"allowCameraUpload": "0",
				"allowChannels":     "0",
				"filterMovies":      "",
				"filterTelevision":  "",
				"filterMusic":       "",
			},
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body=%#v, want %#v", body, want)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	err := New(server.URL, "plexctl-test", server.Client()).Invite(context.Background(), token, InviteRequest{
		MachineIdentifier: "machine-1", InvitedEmail: "friend@example.com", LibrarySectionIDs: []int{7, 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d, want one", requests)
	}
}

func TestSharingUpdateReplacesOnlyRequestedLibraries(t *testing.T) {
	token := "sharing-token"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPut || r.URL.Path != "/api/servers/machine-1/shared_servers/99" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/json")
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type=%q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"server_id": "machine-1",
			"shared_server": map[string]any{
				"library_section_ids": []any{float64(7), float64(8)},
			},
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body=%#v, want full replacement payload %#v", body, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := New(server.URL, "plexctl-test", server.Client()).UpdateShare(context.Background(), token, "machine-1", 99, []int{7, 8})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d, want exactly one PUT without grant fetch or retry", requests)
	}
}

func TestSharingRemoveSendsExactBodylessDelete(t *testing.T) {
	token := "sharing-token"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/servers/machine-1/shared_servers/99" {
			http.NotFound(w, r)
			return
		}
		assertPlexHeaders(t, r, token, "application/json")
		if r.Body == nil {
			t.Fatal("DELETE request body is nil; want a readable empty body")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) != 0 {
			t.Fatalf("DELETE body=%q, want no request body", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := New(server.URL, "plexctl-test", server.Client()).RemoveShare(context.Background(), token, "machine-1", 99)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d, want exactly one DELETE", requests)
	}
}

func TestSharingRemoveRejectsInvalidInputWithoutRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := New(server.URL, "plexctl-test", server.Client())

	for _, tc := range []struct {
		name    string
		machine string
		shareID int
	}{
		{name: "empty machine", shareID: 99},
		{name: "nonpositive share", machine: "machine-1", shareID: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := client.RemoveShare(context.Background(), "sharing-token", tc.machine, tc.shareID); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want no request for invalid removal", requests)
	}
}

func TestSharingRemoveReturnsTypedStatusError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()

			err := New(server.URL, "plexctl-test", server.Client()).RemoveShare(context.Background(), "sharing-token", "machine-1", 99)
			var statusErr *HTTPError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status || statusErr.Method != http.MethodDelete || statusErr.Path != "/api/servers/machine-1/shared_servers/99" {
				t.Fatalf("error=%v, want typed DELETE status error for %d", err, status)
			}
		})
	}
}

func TestSharingUpdateRejectsInvalidInputWithoutRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := New(server.URL, "plexctl-test", server.Client())

	for _, tc := range []struct {
		name    string
		machine string
		shareID int
		ids     []int
	}{
		{name: "empty machine", shareID: 99, ids: []int{7}},
		{name: "nonpositive share", machine: "machine-1", shareID: 0, ids: []int{7}},
		{name: "empty grants", machine: "machine-1", shareID: 99},
		{name: "nonpositive grant", machine: "machine-1", shareID: 99, ids: []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := client.UpdateShare(context.Background(), "sharing-token", tc.machine, tc.shareID, tc.ids); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("requests=%d, want no request for invalid update", requests)
	}
}

func TestSharingInviteReturnsConflictStatusWithoutRetry(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(status)
			}))
			defer server.Close()

			err := New(server.URL, "plexctl-test", server.Client()).Invite(context.Background(), "sharing-token", InviteRequest{
				MachineIdentifier: "machine-1", InvitedEmail: "friend@example.com", LibrarySectionIDs: []int{7},
			})
			var statusErr *HTTPError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status || statusErr.Method != http.MethodPost || statusErr.Path != "/api/servers/machine-1/shared_servers" {
				t.Fatalf("error=%v, want typed POST status error for %d", err, status)
			}
			if requests != 1 {
				t.Fatalf("requests=%d, want no retry", requests)
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
