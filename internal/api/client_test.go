package api

import (
	"context"
	"crypto/tls"
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

// An oversized body must fail loudly instead of being silently truncated into
// a confusing "unexpected end of JSON input" decode error.
func TestOversizedResponseIsReportedNotTruncated(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"title":"` + strings.Repeat("x", 3<<20) + `"}`)); err != nil {
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
