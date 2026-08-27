package plexauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginCreatesAndPollsPin(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins" {
			w.Write([]byte(`{"id":42,"code":"ABCD"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v2/pins/42" {
			calls++
			w.Write([]byte(`{"id":42,"code":"ABCD","authToken":"token-123"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	client := New(s.URL, "plexctl-test", &http.Client{})
	result, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Token != "token-123" || result.Code != "ABCD" || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
	if result.LinkURL != "https://app.plex.tv/auth#?clientID=plexctl-test&code=ABCD" {
		t.Fatalf("unexpected auth URL: %s", result.LinkURL)
	}
}
