package sharinghistory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathUsesExplicitEnvironmentOverride(t *testing.T) {
	const override = "/tmp/custom-history.db"
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", override)

	if got := Path(); got != override {
		t.Fatalf("Path() = %q, want exact override %q", got, override)
	}
}

func TestPathUsesXDGDataHome(t *testing.T) {
	xdgDataHome := t.TempDir()
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", "")
	t.Setenv("XDG_DATA_HOME", xdgDataHome)

	want := filepath.Join(xdgDataHome, "plexctl", "sharing-history.db")
	if got := Path(); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestPathFallsBackToConfigDirectory(t *testing.T) {
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", "")
	t.Setenv("XDG_DATA_HOME", "")

	configHome, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configHome, "plexctl", "sharing-history.db")
	if got := Path(); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}
