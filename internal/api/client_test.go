package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientHeadersAndJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range map[string]string{"Accept": "application/json", "X-Plex-Token": "secret", "X-Plex-Client-Identifier": "plexctl", "X-Plex-Pms-Api-Version": "1"} {
			if r.Header.Get(k) != v {
				t.Errorf("%s=%q", k, r.Header.Get(k))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer s.Close()
	c, e := New(s.URL, "secret", nil)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]bool
	if e = c.Do(context.Background(), "GET", "/identity", nil, nil, &v); e != nil {
		t.Fatal(e)
	}
	if !v["ok"] {
		t.Fatal(v)
	}
}
func TestHTTPErrorDoesNotExposeToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`X-Plex-Token=secret`))
	}))
	defer s.Close()
	c, _ := New(s.URL, "secret", nil)
	e := c.Do(context.Background(), "GET", "/identity", nil, nil, nil)
	if e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatalf("unsafe error: %v", e)
	}
}
