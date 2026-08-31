package authstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetReportsCorruptTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLEXCTL_TOKENS_FILE", path)
	t.Setenv(envTokenKey("corrupt"), "")
	_, _, err := getFileFallback("corrupt")
	if err == nil || !strings.Contains(err.Error(), "parse token file") {
		t.Fatalf("err=%v, want parse error", err)
	}
}

func TestFileFallbackRoundTripIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "tokens.json")
	t.Setenv("PLEXCTL_TOKENS_FILE", path)
	if err := setFileFallback("account/test", "secret"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := getFileFallback("account/test")
	if err != nil || !ok || got != "secret" {
		t.Fatalf("got=%q ok=%v err=%v", got, ok, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o, want 0600", info.Mode().Perm())
	}
}
