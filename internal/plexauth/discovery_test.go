package plexauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
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

func TestResourceCacheRetainsPreviousCompleteSnapshot(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/xml")
		if calls == 1 {
			_, _ = w.Write([]byte(`<MediaContainer><Device name="expected" clientIdentifier="expected"><Connection uri="https://expected.example:32400"/></Device></MediaContainer>`))
			return
		}
		_, _ = w.Write([]byte(`<MediaContainer><Device name="other" clientIdentifier="other"><Connection uri="https://other.example:32400"/></Device></MediaContainer>`))
	}))
	defer server.Close()

	cache := NewResourceCache()
	client := New(server.URL, "test", &http.Client{})
	if _, err := cache.Resources(context.Background(), client, "token", time.Minute); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	key := client.BaseURL + "\x00" + client.ClientID + "\x00" + tokenCacheKey("token")
	entry := cache.entries[key]
	entry.at = time.Now().Add(-2 * time.Minute)
	cache.entries[key] = entry
	cache.mu.Unlock()
	if _, err := cache.Resources(context.Background(), client, "token", time.Minute); err != nil {
		t.Fatal(err)
	}
	previous, ok := cache.PreviousResources(client, "token", time.Minute)
	if !ok || len(previous) != 1 || previous[0].ClientIdentifier != "expected" {
		t.Fatalf("previous=%+v ok=%t, want expected previous snapshot", previous, ok)
	}
}

func TestResourceCacheSerializesSameKeyRefreshAndRetainsPreRefreshSnapshot(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	refreshStarted := make(chan struct{})
	thirdRequest := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 2 {
			close(refreshStarted)
			<-releaseRefresh
		}
		if call == 3 {
			thirdRequest <- struct{}{}
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<MediaContainer><Device name="server-%d" clientIdentifier="id-%d"/></MediaContainer>`, call, call)
	}))
	defer server.Close()

	cache := NewResourceCache()
	client := New(server.URL, "test", &http.Client{})
	if _, err := cache.Resources(context.Background(), client, "token", time.Minute); err != nil {
		t.Fatal(err)
	}
	key := client.BaseURL + "\x00" + client.ClientID + "\x00" + tokenCacheKey("token")
	cache.mu.Lock()
	entry := cache.entries[key]
	entry.at = time.Now().Add(-2 * time.Minute)
	cache.entries[key] = entry
	cache.mu.Unlock()

	results := make(chan error, 2)
	go func() { _, err := cache.Resources(context.Background(), client, "token", time.Minute); results <- err }()
	<-refreshStarted
	go func() { _, err := cache.Resources(context.Background(), client, "token", time.Minute); results <- err }()
	prematureSecondRefresh := false
	select {
	case <-thirdRequest:
		prematureSecondRefresh = true
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRefresh)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if prematureSecondRefresh {
		t.Fatal("same-key refresh issued a second discovery request")
	}
	previous, ok := cache.PreviousResources(client, "token", time.Minute)
	if !ok || len(previous) != 1 || previous[0].ClientIdentifier != "id-1" {
		t.Fatalf("previous=%+v ok=%t, want pre-refresh snapshot id-1", previous, ok)
	}
}

func TestResourceCacheRefreshDoesNotInheritInitiatorCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		close(requestStarted)
		<-releaseResponse
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<MediaContainer><Device name="fresh" clientIdentifier="fresh"/></MediaContainer>`))
	}))
	defer server.Close()

	cache := NewResourceCache()
	client := New(server.URL, "test", &http.Client{})
	initiator, cancel := context.WithCancel(context.Background())
	initiatorResult := make(chan error, 1)
	go func() { _, err := cache.Resources(initiator, client, "token", time.Minute); initiatorResult <- err }()
	<-requestStarted

	waiterResult := make(chan error, 1)
	go func() {
		_, err := cache.Resources(context.Background(), client, "token", time.Minute)
		waiterResult <- err
	}()
	cancel()
	ownerBlocked := false
	select {
	case err := <-initiatorResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("initiator err=%v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		ownerBlocked = true
	}
	close(releaseResponse)
	if ownerBlocked {
		t.Fatal("refresh owner remained blocked after its context was canceled")
	}
	if err := <-waiterResult; err != nil {
		t.Fatalf("healthy waiter inherited initiator cancellation: %v", err)
	}
}

func TestResourcesCacheUsesTTLAndSeparatesTokens(t *testing.T) {
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/resources" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<MediaContainer><Device name="server-%d" clientIdentifier="id-%d" provides="server" owned="1"><Connection uri="https://example.invalid" protocol="https" /></Device></MediaContainer>`, calls, calls)
	}))
	defer s.Close()
	cache := NewResourceCache()
	c := New(s.URL, "test", &http.Client{})
	first, err := cache.Resources(context.Background(), c, "token-a", time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first resources=%+v err=%v", first, err)
	}
	second, err := cache.Resources(context.Background(), c, "token-a", time.Minute)
	if err != nil || len(second) != 1 || calls != 1 {
		t.Fatalf("cached resources=%+v err=%v calls=%d", second, err, calls)
	}
	if _, err := cache.Resources(context.Background(), c, "token-b", time.Minute); err != nil {
		t.Fatalf("second token discovery: %v", err)
	}
	if calls != 2 {
		t.Fatalf("token cache entries were not separated: calls=%d", calls)
	}
	if _, err := cache.Resources(context.Background(), c, "token-a", time.Nanosecond); err != nil {
		t.Fatalf("expired discovery: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expired cache was reused: calls=%d", calls)
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
			if got := r.URL.Query().Get("includeHttps"); got != "1" {
				http.NotFound(w, r)
				return
			}
			if got := r.URL.Query().Get("includeRelay"); got != "1" {
				http.NotFound(w, r)
				return
			}
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

func TestResourcesRetriesTransientJSONFallbackFailure(t *testing.T) {
	var jsonCalls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/api/v2/resources" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("includeHttps") != "1" || r.URL.Query().Get("includeRelay") != "1" {
			http.NotFound(w, r)
			return
		}
		jsonCalls++
		if jsonCalls == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"name":"Recovered server","clientIdentifier":"recovered","provides":"server"}]`))
	}))
	defer s.Close()

	got, err := New(s.URL, "test", &http.Client{}).Resources(context.Background(), "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ClientIdentifier != "recovered" || jsonCalls != 2 {
		t.Fatalf("resources=%+v jsonCalls=%d, want recovered resource after second attempt", got, jsonCalls)
	}
}

func TestResourcesDoesNotRetryAuthenticationFailure(t *testing.T) {
	var jsonCalls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		jsonCalls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer s.Close()

	_, err := New(s.URL, "test", &http.Client{}).Resources(context.Background(), "token")
	if err == nil || jsonCalls != 1 {
		t.Fatalf("err=%v jsonCalls=%d, want immediate authentication failure", err, jsonCalls)
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
