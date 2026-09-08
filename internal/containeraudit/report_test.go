package containeraudit_test

import (
	"reflect"
	"testing"

	"github.com/keithah/plexctl/internal/containeraudit"
)

func TestAnalyzeRequiresCompleteZeroSetBeforeMarkingEmptyAndDeduplicatesKeys(t *testing.T) {
	report, err := containeraudit.Analyze([]containeraudit.Container{
		{ID: "collection-2", Kind: containeraudit.KindCollection, Title: "Incomplete", ItemsComplete: false},
		{ID: "playlist-1", Kind: containeraudit.KindPlaylist, Title: "Mixed", ItemsComplete: true, Items: []containeraudit.Item{{RatingKey: "rk-2"}, {RatingKey: "rk-1"}, {RatingKey: "rk-2"}}},
		{ID: "collection-1", Kind: containeraudit.KindCollection, Title: "Empty", ItemsComplete: true},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	want := []containeraudit.Candidate{
		{ID: "collection-1", Kind: containeraudit.KindCollection, Title: "Empty", ItemsComplete: true, Empty: true},
		{ID: "collection-2", Kind: containeraudit.KindCollection, Title: "Incomplete", ItemsComplete: false, Empty: false},
		{ID: "playlist-1", Kind: containeraudit.KindPlaylist, Title: "Mixed", ItemsComplete: true, ItemRatingKeys: []string{"rk-1", "rk-2"}},
	}
	if !reflect.DeepEqual(report.Candidates, want) {
		t.Fatalf("Candidates = %#v, want %#v", report.Candidates, want)
	}
}

func TestAnalyzeRejectsBlankOrIncompleteOpaqueIdentifiers(t *testing.T) {
	for _, containers := range [][]containeraudit.Container{
		{{ID: "", Kind: containeraudit.KindPlaylist}},
		{{ID: "playlist-1", Kind: containeraudit.KindPlaylist, ItemsComplete: true, Items: []containeraudit.Item{{RatingKey: " "}}}},
	} {
		if _, err := containeraudit.Analyze(containers); err == nil {
			t.Fatal("Analyze() succeeded for blank or incomplete opaque identifier")
		}
	}
}
