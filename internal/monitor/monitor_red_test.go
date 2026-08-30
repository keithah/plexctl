package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/pms"
)

func TestHandlerDoesNotDoubleDecode(t *testing.T) {
	cases := []struct {
		path        string
		wantAccount string
		wantServer  string
	}{
		{"/plex/acct/100%25", "acct", "100%"},
		{"/plex/acct/a%252Fb", "acct", "a%2Fb"},
		{"/plex/acct/Stark%20Tower%20v4", "acct", "Stark Tower v4"},
	}
	for _, tc := range cases {
		var gotAccount, gotServer string
		h := Handler{Resolve: func(a, s string) (*pms.Client, error) {
			gotAccount, gotServer = a, s
			return nil, errSentinel
		}}
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if gotAccount != tc.wantAccount || gotServer != tc.wantServer {
			t.Errorf("%s: got %q/%q want %q/%q (status %d body %s)", tc.path, gotAccount, gotServer, tc.wantAccount, tc.wantServer, r.Code, r.Body)
		}
		if r.Code != http.StatusNotFound {
			// Resolve returns errSentinel -> 404; if we get 400 the double-decode rejected it
			t.Errorf("%s: status %d want 404 (double-decode produced 400)", tc.path, r.Code)
		}
	}
}

var errSentinel = errString("sentinel")

type errString string

func (e errString) Error() string { return string(e) }

func TestHandlerReturns500OnNilClientWithoutPanic(t *testing.T) {
	h := Handler{Resolve: func(string, string) (*pms.Client, error) { return nil, nil }}
	r := httptest.NewRecorder()
	// Should not panic
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("panic on nil client: %v", v)
		}
	}()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/a/b", nil))
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status %d want 500, body %s", r.Code, r.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["classification"] != "configuration" {
		t.Errorf("classification %v want configuration", body["classification"])
	}
}

func TestHandlerErrorResponseHasOKFalse(t *testing.T) {
	h := Handler{}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/a", nil))
	body := r.Body.String()
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("error body missing ok:false: %s", body)
	}
}
