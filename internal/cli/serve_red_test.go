package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/keithah/plexctl/internal/config"
)

func TestResolveServeTargetFailsClosedOnUnknownServer(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		ServersV2: map[string]config.ServerProfile{"real": {Account: "real", Name: "real", URL: "http://127.0.0.1:9"}},
		Accounts:  map[string]config.Account{"real": {Username: "real", TokenKey: "k"}},
	}
	b, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0600)
	t.Setenv("PLEXCTL_CONFIG", cfgPath)
	t.Setenv("TOK", "x")

	o := &options{timeout: 0}
	if _, err := resolveServeTarget(o, "real", "unknown"); err == nil {
		t.Fatal("unknown server should be rejected")
	}
}

func TestResolveServeTargetFailsClosedOnLegacyServer(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		Servers: map[string]config.Server{"legacy": {URL: "http://127.0.0.1:9", TokenEnv: "TOK"}},
		ServersV2: map[string]config.ServerProfile{
			"real": {Account: "real", Name: "real", URL: "http://127.0.0.1:9"},
		},
		Accounts: map[string]config.Account{"real": {Username: "real", TokenKey: "k"}},
	}
	b, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0600)
	t.Setenv("PLEXCTL_CONFIG", cfgPath)
	t.Setenv("TOK", "x")

	o := &options{}
	if _, err := resolveServeTarget(o, "anything", "legacy"); err == nil {
		t.Fatal("legacy server not in V2 should be rejected (old guard skipped check)")
	}
}

func TestResolveServeTargetRejectsWrongAccount(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		ServersV2: map[string]config.ServerProfile{"myserver": {Account: "real", Name: "myserver", URL: "http://127.0.0.1:9"}},
		Accounts: map[string]config.Account{
			"real":  {Username: "real", TokenKey: "k1"},
			"other": {Username: "other", TokenKey: "k2"},
		},
	}
	b, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0600)
	t.Setenv("PLEXCTL_CONFIG", cfgPath)

	o := &options{}
	if _, err := resolveServeTarget(o, "other", "myserver"); err == nil {
		t.Fatal("wrong account should be rejected")
	}
}

func TestResolveServeTargetAcceptsCorrectAccountReachesConfigured(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		ServersV2: map[string]config.ServerProfile{"myserver": {Account: "real", Name: "myserver", URL: "http://127.0.0.1:9"}},
		Accounts:  map[string]config.Account{"real": {Username: "real", TokenKey: "TOK"}},
	}
	b, _ := json.Marshal(cfg)
	os.WriteFile(cfgPath, b, 0600)
	t.Setenv("PLEXCTL_CONFIG", cfgPath)
	// No TOK set — configured will fail on token lookup, but account check must pass first
	// So error should be about token, not about account mismatch
	o := &options{}
	_, err := resolveServeTarget(o, "real", "myserver")
	if err == nil {
		t.Fatal("expected token error, got nil")
	}
	if err.Error() == `server "myserver" belongs to account "real"` || err.Error() == `server "myserver" is not configured` {
		t.Fatalf("account check should have passed, got %v", err)
	}
}
