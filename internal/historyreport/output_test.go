package historyreport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportRejectsJSONAndUnknownExtensionsBeforeOpeningFile(t *testing.T) {
	for _, name := range []string{"history.json", "history.txt", "history.JSONL", "history.jsonl.bak"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)

			err := Export(path, []View{exportTestView()})
			if err == nil {
				t.Fatal("Export() error = nil, want unsupported-extension error")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("output path was opened or created: stat error = %v, want not exist", err)
			}
		})
	}
}

func TestCSVExportWritesHeaderOnlyForNewOrEmptyFile(t *testing.T) {
	for _, setup := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{name: "new file", setup: func(t *testing.T, path string) {}},
		{name: "empty file", setup: func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(setup.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history.csv")
			setup.setup(t, path)

			if err := Export(path, []View{exportTestView()}); err != nil {
				t.Fatalf("Export() error = %v", err)
			}

			rows := readCSVFile(t, path)
			if len(rows) != 2 {
				t.Fatalf("CSV row count = %d, want 2: %#v", len(rows), rows)
			}
			if got, want := rows[0], exportCSVHeader; !equalStrings(got, want) {
				t.Fatalf("CSV header = %#v, want %#v", got, want)
			}
		})
	}
}

func TestCSVExportAppendsQuotedRowsWithoutSecondHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.csv")
	first := exportTestView()
	second := exportTestView()
	second.RatingKey = "two"
	second.Title = "A, \"quoted\"\nmultiline title"

	if err := Export(path, []View{first}); err != nil {
		t.Fatalf("first Export() error = %v", err)
	}
	if err := Export(path, []View{second}); err != nil {
		t.Fatalf("second Export() error = %v", err)
	}

	rows := readCSVFile(t, path)
	if len(rows) != 3 {
		t.Fatalf("CSV row count = %d, want 3: %#v", len(rows), rows)
	}
	if got := countCSVHeader(rows); got != 1 {
		t.Fatalf("CSV header count = %d, want 1", got)
	}
	if got := rows[2][1]; got != second.Title {
		t.Fatalf("quoted title = %q, want %q", got, second.Title)
	}
}

func TestJSONLExportAppendsOneValidObjectPerLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	first := exportTestView()
	second := exportTestView()
	second.RatingKey = "two"
	second.Title = "Second"

	if err := Export(path, []View{first}); err != nil {
		t.Fatalf("first Export() error = %v", err)
	}
	if err := Export(path, []View{second}); err != nil {
		t.Fatalf("second Export() error = %v", err)
	}

	assertJSONLLines(t, path, 2)
}

func TestJSONLExportSeparatesBatchFromExistingObjectWithoutTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte(`{"rating_key":"existing","title":"Existing"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Export(path, []View{exportTestView()}); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	assertJSONLLines(t, path, 2)
}

func TestEmptyExportDoesNotCreateFile(t *testing.T) {
	for _, name := range []string{"history.csv", "history.jsonl"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)

			if err := Export(path, nil); err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("output path was opened or created: stat error = %v, want not exist", err)
			}
		})
	}
}

func TestRenderedRowsDoNotContainTokenSentinel(t *testing.T) {
	const tokenSentinel = "X-Plex-Token=must-not-render"
	path := filepath.Join(t.TempDir(), "history.jsonl")
	type sourceWithToken struct {
		SourceView
		token string
	}
	source := sourceWithToken{
		SourceView: SourceView{
			RatingKey: "one",
			Title:     "Safe title",
			ViewedAt:  time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC),
		},
		token: tokenSentinel,
	}
	views := NormalizeViews([]SourceView{source.SourceView})

	if err := Export(path, views); err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), tokenSentinel) {
		t.Fatalf("rendered output contains token sentinel: %q", data)
	}
}

var exportCSVHeader = []string{
	"rating_key", "title", "parent_title", "grandparent_title", "media_type",
	"section_id", "section_title", "account_id", "account_title", "viewed_at", "duration",
}

func exportTestView() View {
	duration := 42 * time.Minute
	return View{
		RatingKey:        "one",
		Title:            "A title",
		ParentTitle:      "Season 1",
		GrandparentTitle: "A show",
		MediaType:        "episode",
		SectionID:        "1",
		SectionTitle:     "TV",
		AccountID:        "42",
		AccountTitle:     "Viewer",
		ViewedAt:         time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC),
		Duration:         &duration,
	}
}

func assertJSONLLines(t *testing.T, path string, wantCount int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	if len(lines) != wantCount {
		t.Fatalf("JSONL line count = %d, want %d: %q", len(lines), wantCount, data)
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatalf("line %d is not a JSON object: %v", i+1, err)
		}
		if got["rating_key"] == "" {
			t.Fatalf("line %d has no rating_key: %#v", i+1, got)
		}
	}
}

func readCSVFile(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func countCSVHeader(rows [][]string) int {
	count := 0
	for _, row := range rows {
		if equalStrings(row, exportCSVHeader) {
			count++
		}
	}
	return count
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
