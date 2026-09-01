package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLegacyFileOnlyReadsDefaultTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.py")
	content := "" +
		"DEFAULT_TOKENS = {\n" +
		"    'west-1': 'token-one',\n" +
		"    'east_2': 'token-two',\n" +
		"}\n" +
		"OTHER = {'not_an_account': 'must-not-import'}\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := parseLegacyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["west-1"] != "token-one" || got["east_2"] != "token-two" {
		t.Fatalf("unexpected tokens: %#v", got)
	}
	if _, ok := got["not_an_account"]; ok {
		t.Fatal("parsed token outside DEFAULT_TOKENS")
	}
}

func TestParseLegacyFileRequiresDefaultTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.py")
	if err := os.WriteFile(path, []byte("TOKENS = {'account': 'token'}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseLegacyFile(path); err == nil {
		t.Fatal("expected missing DEFAULT_TOKENS error")
	}
}

func TestImportDoesNotAcceptTokenFlag(t *testing.T) {
	if importCmd().Flags().Lookup("token") != nil {
		t.Fatal("import must not accept secret tokens as command-line arguments")
	}
}
func TestImportFailureIsNonNil(t *testing.T) {
	if err := importFailure(1, 1, 0); err == nil || !strings.Contains(err.Error(), "1 of 1") {
		t.Fatalf("err=%v, want aggregate failure", err)
	}
	if err := importFailure(0, 1, 1); err != nil {
		t.Fatalf("zero failures should return nil, got %v", err)
	}
}
func TestReadImportToken(t *testing.T) {
	got, err := readImportToken(strings.NewReader("  token-from-stdin\n"))
	if err != nil || got != "token-from-stdin" {
		t.Fatalf("got %q, err=%v", got, err)
	}
	if _, err := readImportToken(strings.NewReader("\n")); err == nil {
		t.Fatal("expected empty stdin to fail")
	}
}

func TestTokensFromEnv(t *testing.T) {
	t.Setenv("PLEX_TOKEN_IMPORT_TEST", "secret-value")
	t.Setenv("PLEX_TOKEN_EMPTY_TEST", "")
	got := tokensFromEnv()
	if got["import_test"] != "secret-value" {
		t.Fatalf("unexpected env token map: %#v", got)
	}
	if _, ok := got["empty_test"]; ok {
		t.Fatal("empty token should not be imported")
	}
}
