package libraryintegrity

import (
	"reflect"
	"strings"
	"testing"
)

func TestStorageSummarizesKnownAndUnknownPartSizes(t *testing.T) {
	zero, one := int64(0), int64(1)
	items := []Item{{Identity: Identity{SectionKey: "2", SectionTitle: "TV", RatingKey: "beta"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/2/beta", DeclaredBytes: &one}, {Reference: "/library/parts/2/unknown"}}}}}, {Identity: Identity{SectionKey: "1", SectionTitle: "Films", RatingKey: "alpha"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/1/alpha", DeclaredBytes: &zero}}}}}}
	got, err := Storage(items)
	if err != nil {
		t.Fatal(err)
	}
	want := []StorageSummary{{SectionKey: "1", SectionTitle: "Films", KnownPartCount: 1}, {SectionKey: "2", SectionTitle: "TV", KnownPartCount: 1, KnownBytes: 1, UnknownPartCount: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
func TestUnavailablePartsSelectsOnlyFailedProbes(t *testing.T) {
	items := []Item{{Identity: Identity{SectionKey: "1", RatingKey: "alpha"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/1/notrun"}, {Reference: "/library/parts/1/ok", Probe: ProbeSucceeded}, {Reference: "/library/parts/1/failed", Probe: ProbeFailed}}}}}}
	got, err := UnavailableParts(items)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Identity: items[0].Identity, Fingerprint: "8a8ee940b7ee4f4f", Status: StatusProbeFailed}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
func TestDuplicatePartsUsesOpaqueFingerprintWithoutPath(t *testing.T) {
	const ref = "/library/parts/7/duplicate.mkv"
	items := []Item{{Identity: Identity{SectionKey: "2", RatingKey: "unique"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/7/unique.mkv"}}}}}, {Identity: Identity{SectionKey: "1", RatingKey: "second"}, Media: []Media{{Parts: []Part{{Reference: ref}}}}}, {Identity: Identity{SectionKey: "1", RatingKey: "first"}, Media: []Media{{Parts: []Part{{Reference: ref}}}}}}
	got, err := DuplicateParts(items)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Identity: items[2].Identity, Fingerprint: "3e515b829b25f1e6", Status: StatusDuplicate}, {Identity: items[1].Identity, Fingerprint: "3e515b829b25f1e6", Status: StatusDuplicate}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	for _, c := range got {
		if strings.Contains(strings.Join([]string{c.SectionKey, c.SectionTitle, c.RatingKey, c.Title, c.Fingerprint, string(c.Status)}, "\n"), ref) {
			t.Fatalf("raw reference leaked: %#v", c)
		}
	}
}
func TestSuspiciousPartsClassifiesMalformedMedia(t *testing.T) {
	items := []Item{{Identity: Identity{SectionKey: "2", RatingKey: "no-media"}}, {Identity: Identity{SectionKey: "1", RatingKey: "no-parts"}, Media: []Media{{}}}}
	got, err := SuspiciousParts(items)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{Identity: items[1].Identity, Status: StatusNoParts}, {Identity: items[0].Identity, Status: StatusNoMedia}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
func TestAnalysisRejectsMalformedPartRecords(t *testing.T) {
	for _, ref := range []string{"", "   ", "https://example.invalid/part", "/library/parts/1/../secret"} {
		t.Run(ref, func(t *testing.T) {
			_, err := Storage([]Item{{Identity: Identity{SectionKey: "1", RatingKey: "bad"}, Media: []Media{{Parts: []Part{{Reference: ref}}}}}})
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
func TestAnalysisOrderingDoesNotDependOnInputOrder(t *testing.T) {
	items := []Item{{Identity: Identity{SectionKey: "2", RatingKey: "z"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/2/shared", Probe: ProbeFailed}}}}}, {Identity: Identity{SectionKey: "1", RatingKey: "a"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/2/shared", Probe: ProbeFailed}}}}}}
	reverse := []Item{items[1], items[0]}
	for _, analyze := range []func([]Item) ([]Candidate, error){UnavailableParts, DuplicateParts, SuspiciousParts} {
		first, e1 := analyze(items)
		second, e2 := analyze(reverse)
		if e1 != nil || e2 != nil || !reflect.DeepEqual(first, second) {
			t.Fatalf("non-deterministic: %#v %#v", first, second)
		}
	}
}

func TestAnalysisRejectsUnsafeRawPartReferences(t *testing.T) {
	for _, reference := range []string{
		"/library/parts/1/file name",
		"/library/parts/1/file\tname",
		"/library/parts/1/file\x00name",
		"/library/parts/1/file\x1fname",
		"/library/parts/1/file\u0085name",
		"/library/parts/1/file%",
		"/library/parts/1/file%2",
		"/library/parts/1/file%zz",
	} {
		t.Run(reference, func(t *testing.T) {
			_, err := Storage([]Item{{
				Identity: Identity{SectionKey: "1", RatingKey: "bad"},
				Media:    []Media{{Parts: []Part{{Reference: reference}}}},
			}})
			if err == nil {
				t.Fatal("Storage() accepted an unsafe raw part reference")
			}
		})
	}
}

func TestAnalysisAcceptsPercentEncodedPartReferences(t *testing.T) {
	for _, reference := range []string{
		"/library/parts/1/file%20name.mkv",
		"/library/parts/1/file%23chapter%25.mkv",
		"/library/parts/1/%E2%9C%93.mkv",
	} {
		t.Run(reference, func(t *testing.T) {
			if _, err := Storage([]Item{{
				Identity: Identity{SectionKey: "1", RatingKey: "valid"},
				Media:    []Media{{Parts: []Part{{Reference: reference}}}},
			}}); err != nil {
				t.Fatalf("Storage() rejected valid encoded part reference: %v", err)
			}
		})
	}
}

func TestStorageRejectsDeclaredByteOverflow(t *testing.T) {
	max, one := int64(^uint64(0)>>1), int64(1)
	items := []Item{{
		Identity: Identity{SectionKey: "1", RatingKey: "first"},
		Media:    []Media{{Parts: []Part{{Reference: "/library/parts/1/first", DeclaredBytes: &max}}}},
	}, {
		Identity: Identity{SectionKey: "1", RatingKey: "second"},
		Media:    []Media{{Parts: []Part{{Reference: "/library/parts/1/second", DeclaredBytes: &one}}}},
	}}

	if _, err := Storage(items); err == nil {
		t.Fatal("Storage() succeeded when section byte total overflowed")
	}
}

func TestStorageRejectsConflictingSectionMetadata(t *testing.T) {
	items := []Item{{Identity: Identity{SectionKey: "1", SectionTitle: "Films", RatingKey: "a"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/1/a"}}}}}, {Identity: Identity{SectionKey: "1", SectionTitle: "Movies", RatingKey: "b"}, Media: []Media{{Parts: []Part{{Reference: "/library/parts/1/b"}}}}}}
	if _, err := Storage(items); err == nil {
		t.Fatal("Storage() succeeded with conflicting section metadata")
	}
}
