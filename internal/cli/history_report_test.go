package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/config"
)

func TestHistoryReportCommandIsRegistered(t *testing.T) {
	cmd, _, err := NewRoot().Find([]string{"history", "report"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.CommandPath() != "plexctl history report" {
		t.Fatalf("command path = %q", cmd.CommandPath())
	}
}

func TestSessionsHistoryRemainsAvailable(t *testing.T) {
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions/history/all" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"MediaContainer":{"size":0,"Metadata":[]}}`)
	})
	defer server.Close()
	historyReportConfig(t, server.URL)
	if _, err := run(t, "sessions", "history"); err != nil {
		t.Fatalf("sessions history changed behavior: %v", err)
	}
}

func TestHistoryReportRejectsValidationBeforePMSRequest(t *testing.T) {
	server, requests := historyReportServer(t, nil)
	defer server.Close()
	historyReportConfig(t, server.URL)

	output := filepath.Join(t.TempDir(), "report.csv")
	for _, test := range []struct {
		name string
		args []string
		file bool
	}{
		{"export requires output", []string{"history", "report", "--mode", "export"}, false},
		{"root json is rejected", []string{"--json", "history", "report", "--mode", "summary"}, false},
		{"root json false is rejected", []string{"--json=false", "history", "report", "--mode", "summary"}, false},
		{"invalid mode and extension are rejected", []string{"history", "report", "--mode", "other", "--output", filepath.Join(t.TempDir(), "report.json")}, false},
		{"unwatched rejects older than", []string{"history", "report", "--mode", "unwatched", "--older-than", "24h"}, false},
		{"inactive requires positive duration", []string{"history", "report", "--mode", "inactive", "--older-than", "0s"}, false},
		{"invalid extension creates no output", []string{"history", "report", "--mode", "export", "--output", output + ".json"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := run(t, test.args...)
			if err == nil {
				t.Fatal("report command accepted invalid arguments")
			}
			if test.file {
				if _, statErr := os.Stat(output + ".json"); !os.IsNotExist(statErr) {
					t.Fatalf("validation created output: %v", statErr)
				}
			}
		})
	}
	if got := *requests; got != 0 {
		t.Fatalf("validation made %d PMS requests", got)
	}
}

func TestHistoryExportUsesOnlyHistoryGETAndAppendsCSV(t *testing.T) {
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions/history/all" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"ratingKey":"2","title":"Beta","type":"movie","viewedAt":1700000000,"duration":60000},{"ratingKey":"1","title":"Alpha","type":"movie","viewedAt":1600000000}]}}`)
	})
	defer server.Close()
	historyReportConfig(t, server.URL)
	output := filepath.Join(t.TempDir(), "report.csv")

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "history", "report", "--mode", "export", "--output", output)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" {
		t.Fatalf("export stdout = %q, want empty", stdout)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), historyReportToken) {
		t.Fatalf("export exposed token: %q", content)
	}
	want := "rating_key,title,parent_title,grandparent_title,media_type,section_id,section_title,account_id,account_title,viewed_at,duration\n1,Alpha,,,movie,,,,,2020-09-13T12:26:40Z,\n2,Beta,,,movie,,,,,2023-11-14T22:13:20Z,1m0s\n"
	if string(content) != want {
		t.Fatalf("CSV = %q, want %q", content, want)
	}
}

func TestHistorySummaryUsesOnlyHistoryGETAndStableTable(t *testing.T) {
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions/history/all" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"ratingKey":"2","title":"Beta","viewedAt":1700000000,"librarySectionID":"2","librarySectionTitle":"TV","accountID":2,"accountTitle":"B"},{"ratingKey":"1","title":"Alpha","viewedAt":1600000000,"librarySectionID":"1","librarySectionTitle":"Films","accountID":1,"accountTitle":"A"}]}}`)
	})
	defer server.Close()
	historyReportConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "history", "report", "--mode", "summary")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "account_id\taccount_title\tsection_id\tsection_title\tview_count\tfirst_viewed_at\tlast_viewed_at\ttotal_duration\n1\tA\t1\tFilms\t1\t2020-09-13T12:26:40Z\t2020-09-13T12:26:40Z\t\n2\tB\t2\tTV\t1\t2023-11-14T22:13:20Z\t2023-11-14T22:13:20Z\t\n"
	if stdout != want {
		t.Fatalf("summary = %q, want %q", stdout, want)
	}
}

func TestHistoryReportPaginatesPlaybackHistory(t *testing.T) {
	var starts []string
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions/history/all" {
			http.NotFound(w, r)
			return
		}
		starts = append(starts, r.URL.Query().Get("X-Plex-Container-Start"))
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"offset":0,"totalSize":3,"Metadata":[{"ratingKey":"2","viewedAt":1700000000},{"ratingKey":"1","viewedAt":1600000000}]}}`)
		case "2":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":2,"totalSize":3,"Metadata":[{"ratingKey":"3","viewedAt":1800000000}]}}`)
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})
	defer server.Close()
	historyReportConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "history", "report", "--mode", "summary")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "\t3\t2020-09-13T12:26:40Z\t2027-01-15T08:00:00Z\t") {
		t.Fatalf("summary = %q, want all three history pages", stdout)
	}
	if !reflect.DeepEqual(starts, []string{"0", "2"}) {
		t.Fatalf("history page starts = %v", starts)
	}
}

func TestHistoryUnwatchedUsesOnlySectionAndItemGETs(t *testing.T) {
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status/sessions/history/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"ratingKey":"watched","viewedAt":1700000000}]}}`)
		case "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"7","title":"Films","type":"movie"}]}}`)
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"ratingKey":"watched","title":"Zulu","type":"movie"},{"ratingKey":"unwatched","title":"Alpha","type":"movie"}]}}`)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	historyReportConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "history", "report", "--mode", "unwatched", "--section", "7")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "rating_key\ttitle\tsection_id\tsection_title\tmedia_type\nunwatched\tAlpha\t7\tFilms\tmovie\n"
	if stdout != want {
		t.Fatalf("unwatched = %q, want %q", stdout, want)
	}
}

func TestHistoryInactiveUsesStrictCutoff(t *testing.T) {
	oldNow := historyReportNow
	historyReportNow = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	t.Cleanup(func() { historyReportNow = oldNow })
	server, _ := historyReportServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status/sessions/history/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"ratingKey":"old","viewedAt":1699992799},{"ratingKey":"equal","viewedAt":1699992800}]}}`)
		case "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"7","title":"Films"}]}}`)
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":3,"totalSize":3,"Metadata":[{"ratingKey":"equal","title":"Equal","type":"movie"},{"ratingKey":"old","title":"Old","type":"movie"},{"ratingKey":"never","title":"Never","type":"movie"}]}}`)
		default:
			http.NotFound(w, r)
		}
	})
	defer server.Close()
	historyReportConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "history", "report", "--mode", "inactive", "--older-than", "2h")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "rating_key\ttitle\tsection_id\tsection_title\tmedia_type\tlast_viewed_at\nold\tOld\t7\tFilms\tmovie\t2023-11-14T20:13:19Z\n"
	if stdout != want {
		t.Fatalf("inactive = %q, want %q", stdout, want)
	}
}

const historyReportToken = "history-report-token-sentinel"

func historyReportServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *int) {
	t.Helper()
	requests := new(int)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requests = *requests + 1
		if r.Method != http.MethodGet {
			t.Fatalf("PMS method = %s, want GET", r.Method)
		}
		if got := r.Header.Get("X-Plex-Token"); got != historyReportToken {
			t.Fatalf("PMS token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if handler != nil {
			handler(w, r)
		}
	})), requests
}

func historyReportConfig(t *testing.T, url string) {
	t.Helper()
	t.Setenv("PLEXCTL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("HISTORY_REPORT_TOKEN", historyReportToken)
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: url, TokenEnv: "HISTORY_REPORT_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
}

func captureHistoryReportStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	_ = w.Close()
	os.Stdout = old
	var out bytes.Buffer
	if _, copyErr := out.ReadFrom(r); copyErr != nil {
		t.Fatal(copyErr)
	}
	return out.String(), err
}
