package sessiondiagnostics_test

import (
	"reflect"
	"testing"

	"github.com/keithah/plexctl/internal/sessiondiagnostics"
)

func TestAnalyzePreservesSafeFieldsClassifiesAndAggregatesStably(t *testing.T) {
	report, err := sessiondiagnostics.Analyze([]sessiondiagnostics.Session{
		{SessionID: "s-2", Title: "Episode", GrandparentTitle: "Show", ParentTitle: "Season 1", UserID: "u-2", UserTitle: "Bob", ClientID: "c-2", ClientTitle: "TV", ClientPlatform: "Roku", Decision: sessiondiagnostics.DecisionTranscode},
		{SessionID: "s-1", Title: "Movie", UserID: "u-1", UserTitle: "Ada", ClientID: "c-1", ClientTitle: "Web", ClientPlatform: "Chrome", Decision: sessiondiagnostics.DecisionDirectPlay},
		{SessionID: "s-3", Title: "Album", UserID: "u-3", UserTitle: "Cam", ClientID: "c-3", ClientTitle: "Phone", ClientPlatform: "iOS", Decision: sessiondiagnostics.DecisionDirectStream},
		{SessionID: "s-4", Title: "Song", UserID: "u-4", UserTitle: "Dee", ClientID: "c-4", ClientTitle: "Tablet", ClientPlatform: "Android", Decision: "unexpected"},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	wantRows := []sessiondiagnostics.Row{
		{SessionID: "s-1", Title: "Movie", UserID: "u-1", UserTitle: "Ada", ClientID: "c-1", ClientTitle: "Web", ClientPlatform: "Chrome", Decision: sessiondiagnostics.DecisionDirectPlay},
		{SessionID: "s-2", Title: "Episode", GrandparentTitle: "Show", ParentTitle: "Season 1", UserID: "u-2", UserTitle: "Bob", ClientID: "c-2", ClientTitle: "TV", ClientPlatform: "Roku", Decision: sessiondiagnostics.DecisionTranscode},
		{SessionID: "s-3", Title: "Album", UserID: "u-3", UserTitle: "Cam", ClientID: "c-3", ClientTitle: "Phone", ClientPlatform: "iOS", Decision: sessiondiagnostics.DecisionDirectStream},
		{SessionID: "s-4", Title: "Song", UserID: "u-4", UserTitle: "Dee", ClientID: "c-4", ClientTitle: "Tablet", ClientPlatform: "Android", Decision: sessiondiagnostics.DecisionUnknown},
	}
	if !reflect.DeepEqual(report.Rows, wantRows) {
		t.Fatalf("Rows = %#v, want %#v", report.Rows, wantRows)
	}
	wantCounts := []sessiondiagnostics.DecisionCount{
		{Decision: sessiondiagnostics.DecisionDirectPlay, Count: 1},
		{Decision: sessiondiagnostics.DecisionDirectStream, Count: 1},
		{Decision: sessiondiagnostics.DecisionTranscode, Count: 1},
		{Decision: sessiondiagnostics.DecisionUnknown, Count: 1},
	}
	if !reflect.DeepEqual(report.DecisionCounts, wantCounts) {
		t.Fatalf("DecisionCounts = %#v, want %#v", report.DecisionCounts, wantCounts)
	}
}

func TestAnalyzeRejectsBlankRequiredSessionIdentity(t *testing.T) {
	_, err := sessiondiagnostics.Analyze([]sessiondiagnostics.Session{{Title: "Movie"}})
	if err == nil {
		t.Fatal("Analyze() succeeded for blank session identity")
	}
}

func TestAnalyzeRejectsDuplicateSessionIdentity(t *testing.T) {
	_, err := sessiondiagnostics.Analyze([]sessiondiagnostics.Session{
		{SessionID: "session-1", Title: "First"},
		{SessionID: "session-1", Title: "Second"},
	})
	if err == nil {
		t.Fatal("Analyze() succeeded for duplicate session identity")
	}
}
