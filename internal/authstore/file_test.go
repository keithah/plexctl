package authstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
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

func TestHeadlessMissingKeyIsNotFound(t *testing.T) {
	keyring.MockInitWithError(errors.New("secret service unavailable"))
	t.Setenv("PLEXCTL_TOKENS_FILE", filepath.Join(t.TempDir(), "tokens.json"))
	_, err := Get("account/missing")
	if !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestHeadlessDeleteFileFallbackSucceeds(t *testing.T) {
	keyring.MockInitWithError(errors.New("secret service unavailable"))
	t.Setenv("PLEXCTL_TOKENS_FILE", filepath.Join(t.TempDir(), "tokens.json"))
	if err := setFileFallback("account/delete", "value"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("account/delete"); err != nil {
		t.Fatalf("err=%v", err)
	}
	if _, ok, err := getFileFallback("account/delete"); err != nil || ok {
		t.Fatalf("fallback remains: ok=%v err=%v", ok, err)
	}
}
