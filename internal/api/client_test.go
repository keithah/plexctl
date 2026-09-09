package api

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
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
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
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
		if _, err := w.Write([]byte(`X-Plex-Token=secret`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	c, _ := New(s.URL, "secret", nil)
	e := c.Do(context.Background(), "GET", "/identity", nil, nil, nil)
	if e == nil || strings.Contains(e.Error(), "secret") {
		t.Fatalf("unsafe error: %v", e)
	}
	var httpErr *HTTPError
	if !errors.As(e, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized || httpErr.Method != http.MethodGet || httpErr.Path != "/identity" {
		t.Fatalf("HTTPError classification = %#v", httpErr)
	}
}

func TestClientRejectsCrossOriginRedirectBeforeTokenDispatch(t *testing.T) {
	var targetToken string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetToken = r.Header.Get("X-Plex-Token")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer origin.Close()

	c, err := New(origin.URL, "redirect-token-sentinel", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Do(context.Background(), http.MethodGet, "/identity", nil, nil, nil)
	if err == nil {
		t.Fatal("cross-origin redirect succeeded")
	}
	if targetToken != "" {
		t.Fatalf("redirect target received token %q", targetToken)
	}
}

func TestClientAllowsSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		if r.URL.Path != "/final" || r.Header.Get("X-Plex-Token") != "redirect-token-sentinel" {
			t.Fatalf("unexpected redirect request %s token=%q", r.URL.Path, r.Header.Get("X-Plex-Token"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	c, err := New(server.URL, "redirect-token-sentinel", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), http.MethodGet, "/identity", nil, nil, nil); err != nil {
		t.Fatalf("same-origin redirect failed: %v", err)
	}
}

func TestTransportErrorRedactsPMSBaseURLAndToken(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String() + "/private-pms"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	c, err := New(baseURL, "super-secret-token", &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Do(context.Background(), http.MethodGet, "/identity", nil, nil, nil)
	if err == nil {
		t.Fatal("expected unreachable PMS transport error")
	}
	message := err.Error()
	if !strings.Contains(message, "GET /identity") {
		t.Fatalf("error = %q, want method and path context", message)
	}
	if strings.Contains(message, baseURL) || strings.Contains(message, "super-secret-token") {
		t.Fatalf("unsafe transport error: %q", message)
	}
}

// A self-signed TLS server must fail by default and succeed only when the
// caller explicitly opts out of verification.
func TestInsecureTLSIsHonored(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"ok":true}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()

	strict, err := New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = strict.Do(context.Background(), "GET", "/identity", nil, nil, nil); err == nil {
		t.Fatal("expected certificate verification failure with strict TLS")
	}

	relaxed, err := New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	relaxed.SetInsecureTLS(true)
	if err = relaxed.Do(context.Background(), "GET", "/identity", nil, nil, nil); err != nil {
		t.Fatalf("expected success with insecure TLS enabled: %v", err)
	}

	tr, ok := relaxed.HTTP.Transport.(*http.Transport)
	if !ok || tr.TLSClientConfig == nil || !tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatalf("transport did not carry InsecureSkipVerify: %#v", relaxed.HTTP.Transport)
	}
	var _ = tls.Config{}
}

func TestExpectedClientDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "broken pipe", err: syscall.EPIPE, want: true},
		{name: "connection reset", err: syscall.ECONNRESET, want: true},
		{name: "unrelated write error", err: errors.New("disk full"), want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := expectedClientDisconnect(tc.err); got != tc.want {
				t.Errorf("expectedClientDisconnect(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestHTTPErrorHidesUntrustedResponseDetail(t *testing.T) {
	err := (&HTTPError{StatusCode: http.StatusInternalServerError, Method: http.MethodGet, Path: "/library/sections/1", Detail: "http://private-pms.invalid Authorization: Bearer secret"}).Error()
	for _, forbidden := range []string{"private-pms", "Authorization", "secret"} {
		if strings.Contains(err, forbidden) {
			t.Fatalf("HTTP error leaked %q: %q", forbidden, err)
		}
	}
}

func expectedClientDisconnect(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

// An oversized body must fail loudly instead of being silently truncated into
// a confusing "unexpected end of JSON input" decode error.
func TestOversizedResponseIsReportedNotTruncated(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"title":"` + strings.Repeat("x", 3<<20) + `"}`)); err != nil && !expectedClientDisconnect(err) {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	c, err := New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	err = c.Do(context.Background(), "GET", "/library/sections/1/all", nil, nil, &out)
	if err == nil {
		t.Fatal("expected an error for an oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want an explicit size-limit error", err)
	}
}

// A body that fits exactly within the cap must still succeed.
func TestResponseAtSizeLimitSucceeds(t *testing.T) {
	body := `{"title":"` + strings.Repeat("x", int(maxResponseBytes)-12) + `"}`
	if int64(len(body)) != maxResponseBytes {
		t.Fatalf("test body is %d bytes, want exactly %d", len(body), maxResponseBytes)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	c, err := New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := c.Do(context.Background(), "GET", "/identity", nil, nil, &out); err != nil {
		t.Fatalf("body exactly at the limit should succeed, got %v", err)
	}
}
