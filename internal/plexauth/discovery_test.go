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
			if _, err := w.Write([]byte(`{"id":7,"username":"alice","email":"alice@example.com"}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/api/v2/resources":
			if _, err := w.Write([]byte(`[{"name":"Living","clientIdentifier":"m1","accessToken":"server-token","owned":true,"connections":[{"uri":"http://living:32400","local":true,"relay":false},{"uri":"https://relay","local":false,"relay":true}]},{"name":"Office","clientIdentifier":"m2","owned":true,"connections":[{"uri":"http://office:32400","local":true}]}]`)); err != nil {
				t.Errorf("write response: %v", err)
			}
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
			if _, err := w.Write([]byte(`<Error code="401"/>`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.URL.Path == "/api/v2/resources" {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`[{"name":"JSON server","clientIdentifier":"json1"}]`)); err != nil {
				t.Errorf("write response: %v", err)
			}
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

// A Plex account also returns players and controllers. Those have no PMS API,
// so discovery must not treat them as servers.
func TestResourcesSkipNonServerDevices(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(`<MediaContainer>` +
			`<Device name="My Server" clientIdentifier="srv1" provides="server"><Connection uri="https://srv1.plex.direct:32400"/></Device>` +
			`<Device name="Phone" clientIdentifier="phone1" provides="client,player"><Connection uri="http://10.0.0.99:32500" local="1"/></Device>` +
			`<Device name="Web" clientIdentifier="web1" provides="controller"><Connection uri="http://10.0.0.50:32400" local="1"/></Device>` +
			`</MediaContainer>`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	got, err := New(s.URL, "test", &http.Client{}).Resources(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ClientIdentifier != "srv1" {
		t.Fatalf("resources=%+v, want only the server device", got)
	}
}

func TestResourcesSkipNonServerDevicesFromJSON(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[` +
			`{"name":"My Server","clientIdentifier":"srv1","provides":"server","connections":[{"uri":"https://srv1.plex.direct:32400"}]},` +
			`{"name":"Phone","clientIdentifier":"phone1","provides":"client,player","connections":[{"uri":"http://10.0.0.99:32500","local":true}]}` +
			`]`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	got, err := New(s.URL, "test", &http.Client{}).Resources(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ClientIdentifier != "srv1" {
		t.Fatalf("resources=%+v, want only the server device", got)
	}
}

// Older payloads omit "provides"; those resources must be kept so real servers
// are not silently dropped.
func TestResourcesKeepDevicesWithoutProvides(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		if _, err := w.Write([]byte(`<MediaContainer><Device name="Legacy" clientIdentifier="old1"><Connection uri="https://old1.plex.direct:32400"/></Device></MediaContainer>`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	got, err := New(s.URL, "test", &http.Client{}).Resources(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ClientIdentifier != "old1" {
		t.Fatalf("resources=%+v, want the provides-less device kept", got)
	}
}
