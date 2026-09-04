package sharinghistory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestAppendAndList(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "private", "sharing-history.db")
	t.Setenv("PLEXCTL_SHARING_HISTORY_DB", databasePath)
	history := Open(Path())
	ctx := context.Background()

	older := Record{
		RemovedAt:              time.Date(2026, time.September, 4, 15, 30, 0, 123456789, time.FixedZone("PDT", -7*60*60)),
		PlexUserID:             101,
		Username:               "older-user",
		Email:                  nil,
		ShareID:                201,
		ServerName:             "Media Server",
		ServerClientIdentifier: "server-older",
		AllLibraries:           false,
		Pending:                false,
		LibrarySectionIDs:      []int{9, 2, 7},
	}
	newer := Record{
		RemovedAt:              time.Date(2026, time.September, 4, 22, 31, 0, 123456789, time.UTC),
		PlexUserID:             102,
		Username:               "newer-user",
		Email:                  stringPointer("newer@example.com"),
		ShareID:                202,
		ServerName:             "Media Server",
		ServerClientIdentifier: "server-newer",
		AllLibraries:           true,
		Pending:                true,
		LibrarySectionIDs:      []int{4, 1, 3},
	}

	if err := history.Append(ctx, older); err != nil {
		t.Fatal(err)
	}
	if err := history.Append(ctx, newer); err != nil {
		t.Fatal(err)
	}
	tiedNewest := newer
	tiedNewest.Username = "tied-newest-user"
	tiedNewest.ShareID = 203
	tiedNewest.Email = nil
	if err := history.Append(ctx, tiedNewest); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %o, want 0600", info.Mode().Perm())
	}
	parentInfo, err := os.Stat(filepath.Dir(databasePath))
	if err != nil {
		t.Fatalf("stat database directory: %v", err)
	}
	if parentInfo.Mode().Perm() != 0o700 {
		t.Fatalf("database directory mode = %o, want 0700", parentInfo.Mode().Perm())
	}

	got, err := history.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("List() returned %d records, want 3", len(got))
	}
	if got[0].Username != tiedNewest.Username || got[1].Username != newer.Username || got[2].Username != older.Username {
		t.Fatalf("List() order = [%q, %q, %q], want [%q, %q, %q]", got[0].Username, got[1].Username, got[2].Username, tiedNewest.Username, newer.Username, older.Username)
	}
	if !got[2].RemovedAt.Equal(older.RemovedAt.UTC()) || got[2].RemovedAt.Location() != time.UTC {
		t.Fatalf("older RemovedAt = %s (%s), want UTC %s", got[2].RemovedAt, got[2].RemovedAt.Location(), older.RemovedAt.UTC())
	}
	if !reflect.DeepEqual(got[2].LibrarySectionIDs, []int{2, 7, 9}) {
		t.Fatalf("older LibrarySectionIDs = %v, want [2 7 9]", got[2].LibrarySectionIDs)
	}
	if !reflect.DeepEqual(got[1].LibrarySectionIDs, []int{1, 3, 4}) {
		t.Fatalf("newer LibrarySectionIDs = %v, want [1 3 4]", got[1].LibrarySectionIDs)
	}
	if got[2].Email != nil {
		t.Fatalf("older Email = %q, want nil", *got[2].Email)
	}

	recordType := reflect.TypeFor[Record]()
	for i := 0; i < recordType.NumField(); i++ {
		field := strings.ToLower(recordType.Field(i).Name)
		if strings.Contains(field, "token") || strings.Contains(field, "secret") || strings.Contains(field, "password") || strings.Contains(field, "url") {
			t.Fatalf("Record exposes secret-like field %q", recordType.Field(i).Name)
		}
	}
}

func TestListMissingDatabaseReturnsEmptyWithoutCreatingFile(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "missing", "sharing-history.db")
	history := Open(databasePath)

	got, err := history.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() returned %d records, want empty result", len(got))
	}
	if _, err := os.Stat(databasePath); !os.IsNotExist(err) {
		t.Fatalf("database exists after List() or stat failed: %v", err)
	}
}

func stringPointer(value string) *string { return &value }
