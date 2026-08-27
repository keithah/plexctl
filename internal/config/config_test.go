package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveModeAndResolve(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	c := Config{Current: "home", Servers: map[string]Server{"home": {URL: "http://plex", TokenEnv: "TOKEN"}}}
	if e := Save(p, c); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	got, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	n, s, e := got.Resolve("")
	if e != nil || n != "home" || s.URL != "http://plex" {
		t.Fatalf("%s %+v %v", n, s, e)
	}
}
