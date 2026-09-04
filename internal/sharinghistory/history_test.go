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

func TestPurgeBeforeKeepsRecordsAtAndAfterCutoff(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "sharing-history.db")
	history := Open(databasePath)
	ctx := context.Background()
	cutoff := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)

	for _, record := range []Record{
		{RemovedAt: cutoff.Add(-time.Nanosecond), Username: "before-cutoff"},
		{RemovedAt: cutoff.In(time.FixedZone("PDT", -7*60*60)), Username: "at-cutoff"},
		{RemovedAt: cutoff.Add(time.Nanosecond), Username: "after-cutoff"},
	} {
		if err := history.Append(ctx, record); err != nil {
			t.Fatalf("append %q: %v", record.Username, err)
		}
	}

	count, err := history.CountBefore(ctx, cutoff.In(time.FixedZone("PDT", -7*60*60)))
	if err != nil {
		t.Fatalf("count before cutoff: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountBefore() = %d, want 1", count)
	}

	deleted, err := history.PurgeBefore(ctx, cutoff.In(time.FixedZone("PDT", -7*60*60)))
	if err != nil {
		t.Fatalf("purge before cutoff: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("PurgeBefore() deleted %d records, want 1", deleted)
	}

	remaining, err := history.List(ctx)
	if err != nil {
		t.Fatalf("list remaining records: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("List() returned %d records after purge, want 2", len(remaining))
	}
	if remaining[0].Username != "after-cutoff" || remaining[1].Username != "at-cutoff" {
		t.Fatalf("remaining users = [%q, %q], want [\"after-cutoff\", \"at-cutoff\"]", remaining[0].Username, remaining[1].Username)
	}
}

func TestPurgeBeforeHandlesLegacyRFC3339NanoTimestamp(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "sharing-history.db")
	history := Open(databasePath)
	ctx := context.Background()

	// Seed a timestamp produced by the pre-Task-3 Append implementation. Its
	// fractional part is variable-width RFC3339Nano and must be compared by
	// instant, not SQLite's lexical TEXT order.
	db, err := history.openDatabase()
	if err != nil {
		t.Fatalf("open history database: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO removed_external_shares (
		removed_at, plex_user_id, username, email, share_id, server_name,
		server_client_identifier, all_libraries, pending, library_section_ids
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"2026-09-04T12:00:00.49Z", 1, "legacy-before-cutoff", nil, 1,
		"server", "server-id", 0, 0, "[]",
	)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close seeded history database: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("seed legacy history row: %v", err)
	}

	cutoff := time.Date(2026, time.September, 4, 12, 0, 0, 495000000, time.UTC)
	count, err := history.CountBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("count before cutoff: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountBefore() = %d, want 1 for legacy timestamp before cutoff", count)
	}

	deleted, err := history.PurgeBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("purge before cutoff: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("PurgeBefore() deleted %d records, want 1 for legacy timestamp before cutoff", deleted)
	}

	remaining, err := history.List(ctx)
	if err != nil {
		t.Fatalf("list remaining records: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("List() returned %d records after purge, want 0", len(remaining))
	}
}

func TestAppendSecuresExistingHistoryDirectory(t *testing.T) {
	parentDirectory := filepath.Join(t.TempDir(), "existing-history")
	if err := os.Mkdir(parentDirectory, 0o755); err != nil {
		t.Fatalf("create existing history directory: %v", err)
	}
	if err := os.Chmod(parentDirectory, 0o755); err != nil {
		t.Fatalf("set existing history directory permissions: %v", err)
	}

	databasePath := filepath.Join(parentDirectory, "sharing-history.db")
	history := Open(databasePath)
	if err := history.Append(context.Background(), Record{}); err != nil {
		t.Fatalf("append removal history: %v", err)
	}

	info, err := os.Stat(parentDirectory)
	if err != nil {
		t.Fatalf("stat history directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("history directory mode = %o, want 0700", info.Mode().Perm())
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
