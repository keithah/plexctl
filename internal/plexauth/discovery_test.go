package plexauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountAndResourceDiscovery(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/user":
			w.Write([]byte(`{"id":7,"username":"alice","email":"alice@example.com"}`))
		case "/api/v2/resources":
			w.Write([]byte(`[{"name":"Living","clientIdentifier":"m1","accessToken":"server-token","owned":true,"connections":[{"uri":"http://living:32400","local":true,"relay":false},{"uri":"https://relay","local":false,"relay":true}]},{"name":"Office","clientIdentifier":"m2","owned":true,"connections":[{"uri":"http://office:32400","local":true}]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(s.URL, "test", &http.Client{})
	u, err := c.User(context.Background(), "token")
	if err != nil || u.ID != 7 {
		t.Fatalf("user=%+v err=%v", u, err)
	}
	r, err := c.Resources(context.Background(), "token")
	if err != nil || len(r) != 2 || len(r[0].Connections) != 2 || r[0].AccessToken != "server-token" {
		t.Fatalf("resources=%+v err=%v", r, err)
	}
}

func TestResourcesPreferLegacyHTTPSConnections(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Path != "/api/resources" {
			t.Errorf("unexpected fallback request")
			return
		}
		if _, err := w.Write([]byte(`<MediaContainer><Device name="SF-Syno" clientIdentifier="m1" accessToken="server-token"><Connection protocol="https" uri="https://m1.plex.direct:32400" local="0" relay="0"/></Device></MediaContainer>`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	c := New(s.URL, "test", &http.Client{})
	r, err := c.Resources(context.Background(), "token")
	if err != nil || len(r) != 1 || r[0].Connections[0].URI != "https://m1.plex.direct:32400" || r[0].Connections[0].Protocol != "https" {
		t.Fatalf("resources=%+v err=%v", r, err)
	}
}

func TestResourcesFallsBackToJSONForUnrelatedXML(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<Error code="401"/>`))
			return
		}
		if r.URL.Path == "/api/v2/resources" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"name":"JSON server","clientIdentifier":"json1"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	c := New(s.URL, "test", &http.Client{})
	r, err := c.Resources(context.Background(), "token")
	if err != nil || len(r) != 1 || r[0].Name != "JSON server" {
		t.Fatalf("resources=%+v err=%v", r, err)
	}
}
