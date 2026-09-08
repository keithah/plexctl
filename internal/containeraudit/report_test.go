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
		{ID: "playlist-1", Kind: containeraudit.KindPlaylist, Title: "Mixed", ItemsComplete: true, ItemRatingKeys: []string{"rk-1", "rk-2"}, DuplicateItemRatingKeys: []string{"rk-2"}},
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

func TestAnalyzeRejectsDuplicateContainerIdentity(t *testing.T) {
	_, err := containeraudit.Analyze([]containeraudit.Container{
		{ID: "container-1", Kind: containeraudit.KindPlaylist, Title: "First"},
		{ID: "container-1", Kind: containeraudit.KindCollection, Title: "Second"},
	})
	if err == nil {
		t.Fatal("Analyze() succeeded for duplicate container identity")
	}
}

func TestAnalyzeNormalizesDuplicateItemKeysIndependentlyOfInputOrder(t *testing.T) {
	first, err := containeraudit.Analyze([]containeraudit.Container{{
		ID: "container-1", Items: []containeraudit.Item{{RatingKey: "item-2"}, {RatingKey: "item-1"}, {RatingKey: "item-2"}},
	}})
	if err != nil {
		t.Fatalf("Analyze() first error = %v", err)
	}
	second, err := containeraudit.Analyze([]containeraudit.Container{{
		ID: "container-1", Items: []containeraudit.Item{{RatingKey: "item-2"}, {RatingKey: "item-2"}, {RatingKey: "item-1"}},
	}})
	if err != nil {
		t.Fatalf("Analyze() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Analyze() reports differ by item input order: first = %#v, second = %#v", first, second)
	}
}
