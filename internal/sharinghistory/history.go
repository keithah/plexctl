// Package sharinghistory stores local records of successful Plex share removals.
package sharinghistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

const timestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

const schema = `
CREATE TABLE IF NOT EXISTS removed_external_shares (
  id INTEGER PRIMARY KEY,
  removed_at TEXT NOT NULL,
  plex_user_id INTEGER NOT NULL,
  username TEXT NOT NULL,
  email TEXT,
  share_id INTEGER NOT NULL,
  server_name TEXT NOT NULL,
  server_client_identifier TEXT NOT NULL,
  all_libraries INTEGER NOT NULL,
  pending INTEGER NOT NULL,
  library_section_ids TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS removed_external_shares_removed_at_idx
  ON removed_external_shares (removed_at DESC, id DESC);`

// Record is a redacted snapshot of a successfully removed external share.
type Record struct {
	RemovedAt              time.Time
	PlexUserID             int64
	Username               string
	Email                  *string
	ShareID                int64
	ServerName             string
	ServerClientIdentifier string
	AllLibraries           bool
	Pending                bool
	LibrarySectionIDs      []int
}

// History persists local share-removal records at path.
type History struct {
	path string
}

// Open creates a handle for history stored at path. The database is opened lazily.
func Open(path string) *History {
	return &History{path: path}
}

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

// Append stores record in the local history.
func (h *History) Append(ctx context.Context, record Record) error {
	db, err := h.openDatabase()
	if err != nil {
		return err
	}
	defer db.Close()

	librarySectionIDs := append([]int(nil), record.LibrarySectionIDs...)
	sort.Ints(librarySectionIDs)
	encodedLibrarySectionIDs, err := json.Marshal(librarySectionIDs)
	if err != nil {
		return fmt.Errorf("encode library section IDs: %w", err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO removed_external_shares (
		removed_at, plex_user_id, username, email, share_id, server_name,
		server_client_identifier, all_libraries, pending, library_section_ids
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTimestamp(record.RemovedAt),
		record.PlexUserID,
		record.Username,
		record.Email,
		record.ShareID,
		record.ServerName,
		record.ServerClientIdentifier,
		boolToInt(record.AllLibraries),
		boolToInt(record.Pending),
		string(encodedLibrarySectionIDs),
	)
	if err != nil {
		return fmt.Errorf("append removal history: %w", err)
	}
	return nil
}

// CountBefore returns the number of records removed strictly before cutoff.
func (h *History) CountBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	db, err := h.openDatabase()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var count int64
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM removed_external_shares WHERE removed_at < ?`,
		formatTimestamp(cutoff),
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count removal history before cutoff: %w", err)
	}
	return count, nil
}

// CountBeforeReadOnly returns the number of records removed strictly before
// cutoff without creating, initializing, migrating, or changing the history
// database. A missing history database has no records.
func (h *History) CountBeforeReadOnly(ctx context.Context, cutoff time.Time) (int64, error) {
	if _, err := os.Stat(h.path); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat removal history: %w", err)
	}

	db, err := h.openReadOnlyDatabase()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT removed_at FROM removed_external_shares`)
	if err != nil {
		return 0, fmt.Errorf("query removal history timestamps: %w", err)
	}
	defer rows.Close()

	var count int64
	for rows.Next() {
		var removedAt string
		if err := rows.Scan(&removedAt); err != nil {
			return 0, fmt.Errorf("scan removal timestamp: %w", err)
		}
		parsedRemovedAt, err := time.Parse(time.RFC3339Nano, removedAt)
		if err != nil {
			return 0, fmt.Errorf("parse removal timestamp: %w", err)
		}
		if parsedRemovedAt.Before(cutoff) {
			count++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate removal history timestamps: %w", err)
	}
	return count, nil
}

// PurgeBefore deletes records removed strictly before cutoff and returns the number deleted.
func (h *History) PurgeBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	db, err := h.openDatabase()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	result, err := db.ExecContext(ctx,
		`DELETE FROM removed_external_shares WHERE removed_at < ?`,
		formatTimestamp(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("purge removal history before cutoff: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count purged removal history: %w", err)
	}
	return deleted, nil
}

// List returns records in deterministic newest-first order.
func (h *History) List(ctx context.Context) ([]Record, error) {
	if _, err := os.Stat(h.path); err != nil {
		if os.IsNotExist(err) {
			return []Record{}, nil
		}
		return nil, fmt.Errorf("stat removal history: %w", err)
	}

	db, err := h.openDatabase()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `SELECT removed_at, plex_user_id, username, email, share_id,
		server_name, server_client_identifier, all_libraries, pending, library_section_ids
		FROM removed_external_shares ORDER BY removed_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list removal history: %w", err)
	}
	defer rows.Close()

	records := []Record{}
	for rows.Next() {
		var record Record
		var removedAt string
		var allLibraries, pending int
		var librarySectionIDs string
		if err := rows.Scan(
			&removedAt,
			&record.PlexUserID,
			&record.Username,
			&record.Email,
			&record.ShareID,
			&record.ServerName,
			&record.ServerClientIdentifier,
			&allLibraries,
			&pending,
			&librarySectionIDs,
		); err != nil {
			return nil, fmt.Errorf("scan removal history: %w", err)
		}
		parsedRemovedAt, err := time.Parse(time.RFC3339Nano, removedAt)
		if err != nil {
			return nil, fmt.Errorf("parse removal timestamp: %w", err)
		}
		if err := json.Unmarshal([]byte(librarySectionIDs), &record.LibrarySectionIDs); err != nil {
			return nil, fmt.Errorf("decode library section IDs: %w", err)
		}
		record.RemovedAt = parsedRemovedAt.UTC()
		record.AllLibraries = allLibraries != 0
		record.Pending = pending != 0
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate removal history: %w", err)
	}
	return records, nil
}

func (h *History) openDatabase() (*sql.DB, error) {
	parentDirectory := filepath.Dir(h.path)
	if err := os.MkdirAll(parentDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create removal history directory: %w", err)
	}
	if err := os.Chmod(parentDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set removal history directory permissions: %w", err)
	}
	file, err := os.OpenFile(h.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create removal history database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close removal history database file: %w", err)
	}
	if err := os.Chmod(h.path, 0o600); err != nil {
		return nil, fmt.Errorf("set removal history database permissions: %w", err)
	}

	db, err := sql.Open("sqlite", h.path)
	if err != nil {
		return nil, fmt.Errorf("open removal history database: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize removal history database: %w", err)
	}
	if err := normalizeTimestamps(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (h *History) openReadOnlyDatabase() (*sql.DB, error) {
	databaseURL := (&url.URL{Scheme: "file", Path: h.path, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open removal history database read-only: %w", err)
	}
	return db, nil
}

func normalizeTimestamps(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin removal timestamp normalization: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, removed_at FROM removed_external_shares`)
	if err != nil {
		return fmt.Errorf("query removal timestamps for normalization: %w", err)
	}
	defer rows.Close()

	type timestampRow struct {
		id        int64
		removedAt string
	}
	var timestamps []timestampRow
	for rows.Next() {
		var timestamp timestampRow
		if err := rows.Scan(&timestamp.id, &timestamp.removedAt); err != nil {
			return fmt.Errorf("scan removal timestamp for normalization: %w", err)
		}
		timestamps = append(timestamps, timestamp)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate removal timestamps for normalization: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close removal timestamps for normalization: %w", err)
	}

	for _, timestamp := range timestamps {
		parsed, err := time.Parse(time.RFC3339Nano, timestamp.removedAt)
		if err != nil {
			return fmt.Errorf("parse removal timestamp for normalization: %w", err)
		}
		formatted := formatTimestamp(parsed)
		if formatted == timestamp.removedAt {
			continue
		}
		if _, err := tx.Exec(`UPDATE removed_external_shares SET removed_at = ? WHERE id = ?`, formatted, timestamp.id); err != nil {
			return fmt.Errorf("normalize removal timestamp: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit removal timestamp normalization: %w", err)
	}
	return nil
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Format(timestampLayout)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
