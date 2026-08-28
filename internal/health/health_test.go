package health

import (
	"context"
	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPing(t *testing.T) {
	s := httptest.NewServer(httpHandler(t, `{"MediaContainer":{"size":0}}`))
	defer s.Close()
	a, _ := api.New(s.URL, "secret", nil)
	r := Ping(context.Background(), pms.New(a))
	if !r.OK || r.Stage != "identity" {
		t.Fatalf("%+v", r)
	}
}
func httpHandler(t *testing.T, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "bad accept", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})
}
