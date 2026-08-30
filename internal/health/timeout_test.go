package health

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
)

// cancelAfterResponse buffers a successful response, then cancels the caller's
// context before the result is classified. This reproduces the real race: the
// PMS call genuinely succeeded, but a parent timeout expired just afterwards.
type cancelAfterResponse struct {
	base   http.RoundTripper
	cancel context.CancelFunc
}

func (t *cancelAfterResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	t.cancel()
	return resp, nil
}

func racingClient(t *testing.T, url string, cancel context.CancelFunc) *pms.Client {
	t.Helper()
	hc := &http.Client{Transport: &cancelAfterResponse{base: http.DefaultTransport, cancel: cancel}}
	a, err := api.New(url, "", hc)
	if err != nil {
		t.Fatal(err)
	}
	return pms.New(a)
}

// A check that actually succeeded must not be relabelled as a timeout just
// because the caller's context was cancelled afterwards. Reporting a healthy
// server as "timeout" is indistinguishable from a genuinely unreachable one.
func TestSuccessfulPingIsNotRelabelledAsTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1"}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := Ping(ctx, racingClient(t, s.URL, cancel))
	if !got.OK || got.Classification != OK {
		t.Fatalf("result=%+v, want a healthy result despite the cancelled context", got)
	}
}

// A real timeout must still be classified as one.
func TestGenuineTimeoutIsStillClassified(t *testing.T) {
	blocked := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	defer func() { close(blocked); s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	got := Ping(ctx, healthClient(t, s.URL))
	if got.OK || got.Classification != Timeout {
		t.Fatalf("result=%+v, want a timeout classification", got)
	}
}
