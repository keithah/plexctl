// Package sharinghistory stores local records of successful Plex share removals.
package sharinghistory

import (
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Path returns the local SQLite database path for sharing removal history.
func Path() string {
	if path := os.Getenv("PLEXCTL_SHARING_HISTORY_DB"); path != "" {
		return path
	}
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return filepath.Join(dataHome, "plexctl", "sharing-history.db")
	}
	configHome, _ := os.UserConfigDir()
	return filepath.Join(configHome, "plexctl", "sharing-history.db")
}
