package pms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestReportMetadataDecodesThumbAndGUIDs(t *testing.T) {
	var metadata Metadata
	if err := json.Unmarshal([]byte(`{
		"ratingKey": "42",
		"title": "Example",
		"type": "movie",
		"year": 2024,
		"thumb": "/library/metadata/42/thumb/123",
		"Guid": [{"id": "plex://movie/example"}]
	}`), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	if metadata.RatingKey != "42" || metadata.Title != "Example" || metadata.Type != "movie" || metadata.Year != 2024 {
		t.Fatalf("existing metadata fields changed: %+v", metadata)
	}
	if metadata.Thumb != "/library/metadata/42/thumb/123" {
		t.Fatalf("thumb = %q", metadata.Thumb)
	}
	if len(metadata.GUID) != 1 || metadata.GUID[0].ID != "plex://movie/example" {
		t.Fatalf("GUID = %+v", metadata.GUID)
	}
}

func TestProbeThumbSendsBoundedAuthenticatedGET(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/library/metadata/42/thumb/123" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Range"); got != "bytes=0-1023" {
			t.Errorf("Range = %q", got)
		}
		if got := r.Header.Get("X-Plex-Token"); got != "test-token" {
			t.Errorf("X-Plex-Token = %q", got)
		}
		if got := r.Header.Get("X-Plex-Client-Identifier"); got == "" {
			t.Error("X-Plex-Client-Identifier is missing")
		}
		if _, err := w.Write(make([]byte, 2048)); err != nil {
			t.Errorf("write probe response: %v", err)
		}
	}))
	defer s.Close()

	client := newProbeClient(t, s.URL, "test-token", nil)
	if err := client.ProbeThumb(context.Background(), "/library/metadata/42/thumb/123"); err != nil {
		t.Fatalf("ProbeThumb: %v", err)
	}
}

func TestProbeThumbReadsAtMostConfiguredRange(t *testing.T) {
	body := &countingReadCloser{remaining: 2048}
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			// A 200 response with a long body models a PMS that ignored Range.
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       body,
		}, nil
	})}

	err := newProbeClient(t, "http://example.invalid", "", hc).ProbeThumb(context.Background(), "/library/metadata/42/thumb/123")
	if err != nil {
		t.Fatalf("ProbeThumb: %v", err)
	}
	if got := body.read; got > 1024 {
		t.Fatalf("probe read %d bytes, want at most 1024", got)
	}
}

func TestProbeThumbReturnsErrors(t *testing.T) {
	t.Run("empty 2xx body", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer s.Close()

		err := newProbeClient(t, s.URL, "", nil).ProbeThumb(context.Background(), "/library/metadata/42/thumb/123")
		if err == nil {
			t.Fatal("ProbeThumb succeeded with empty body")
		}
		if strings.Contains(err.Error(), s.URL) {
			t.Fatalf("error exposed server URL: %v", err)
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer s.Close()

		if err := newProbeClient(t, s.URL, "", nil).ProbeThumb(context.Background(), "/library/metadata/42/thumb/123"); err == nil {
			t.Fatal("ProbeThumb succeeded with HTTP error")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})}
		if err := newProbeClient(t, "http://example.invalid", "", hc).ProbeThumb(context.Background(), "/library/metadata/42/thumb/123"); err == nil {
			t.Fatal("ProbeThumb succeeded with transport error")
		}
	})
}

func TestProbeThumbRejectsUnsafePathsBeforeRequest(t *testing.T) {
	var requests atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("unsafe path made a request")
	}))
	defer s.Close()

	client := newProbeClient(t, s.URL, "", nil)
	for _, path := range []string{
		"",
		"library/metadata/42/thumb/123",
		"https://example.com/library/metadata/42/thumb/123",
		"//example.com/library/metadata/42/thumb/123",
		"/metadata/42/thumb/123",
		"/library/../identity",
		"/library/%2e%2e/identity",
		"/library/metadata/42/thumb/123?",
	} {
		err := client.ProbeThumb(context.Background(), path)
		if err == nil {
			t.Errorf("ProbeThumb(%q) succeeded", path)
			continue
		}
		if strings.Contains(err.Error(), path) && path != "" {
			t.Errorf("ProbeThumb(%q) exposed raw path in error: %v", path, err)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unsafe probes made %d requests", got)
	}
}

type countingReadCloser struct {
	remaining int
	read      int
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	r.remaining -= n
	r.read += n
	return n, nil
}

func (*countingReadCloser) Close() error { return nil }

func newProbeClient(t *testing.T, baseURL, token string, hc *http.Client) *Client {
	t.Helper()
	client, err := api.New(baseURL, token, hc)
	if err != nil {
		t.Fatalf("new api client: %v", err)
	}
	return New(client)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProbePartSendsBoundedGETAndRejectsUnsafePaths(t *testing.T) {
	var requests atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/library/parts/1/file" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Range"); got != "bytes=0-1023" {
			t.Errorf("Range = %q", got)
		}
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer s.Close()

	client := newProbeClient(t, s.URL, "", nil)
	if err := client.probePart(context.Background(), "/library/parts/1/file"); err != nil {
		t.Fatalf("probePart: %v", err)
	}
	for _, path := range []string{"", "/library/metadata/1", "/library/../identity", "/library/parts/1/file?download=1", "https://example.invalid/library/parts/1/file"} {
		if err := client.probePart(context.Background(), path); err == nil {
			t.Errorf("probePart(%q) succeeded", path)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestProbePartReadsAtMostConfiguredRange(t *testing.T) {
	body := &countingReadCloser{remaining: 2048}
	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})}
	if err := newProbeClient(t, "http://example.invalid", "", hc).probePart(context.Background(), "/library/parts/1/file"); err != nil {
		t.Fatalf("probePart: %v", err)
	}
	if got := body.read; got > 1024 {
		t.Fatalf("probe read %d bytes, want at most 1024", got)
	}
}
