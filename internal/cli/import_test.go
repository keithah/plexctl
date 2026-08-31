package cli

import (
	"os"
	"path/filepath"
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
