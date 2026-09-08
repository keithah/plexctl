// Package libraryintegrity provides pure, deterministic media-part integrity analysis.
package libraryintegrity

import (
	"crypto/sha256"
	"fmt"
	"math"
	"net/url"
	pathpkg "path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Identity is the public library identity safe to expose in reports.
type Identity struct {
	SectionKey, SectionTitle, RatingKey, Title string
}

// Item is a source-neutral media record for integrity analysis.
type Item struct {
	Identity Identity
	Media    []Media
}

// Media groups a media item's parts.
type Media struct{ Parts []Part }

// Part contains caller-only raw part data. Reference is never copied into output.
// It must be a relative, clean PMS route of the form
// /library/parts/<part-key>/<file-path>, with neither raw nor decoded
// control characters. Reserved or non-ASCII path characters must be valid
// percent-encoded UTF-8; queries, fragments, authorities, and schemes are not allowed.
type Part struct {
	Reference     string
	DeclaredBytes *int64
	Probe         ProbeState
}

// ProbeState describes a caller-provided media-part probe result.
type ProbeState uint8

const (
	ProbeNotRun ProbeState = iota
	ProbeSucceeded
	ProbeFailed
)

// StorageSummary totals declared part storage per section. KnownPartCount makes
// a known zero-byte part distinguishable from an omitted, unknown size.
type StorageSummary struct {
	SectionKey, SectionTitle              string
	EligibleItemCount, DeclaredMediaCount int
	KnownPartCount, UnknownPartCount      int
	KnownBytes                            int64
}

// Storage summarizes known and unknown declared part sizes by section.
func Storage(items []Item) ([]StorageSummary, error) {
	if err := validateItems(items); err != nil {
		return nil, err
	}
	bySection := make(map[string]StorageSummary)
	for _, item := range items {
		summary, ok := bySection[item.Identity.SectionKey]
		if !ok {
			summary = StorageSummary{SectionKey: item.Identity.SectionKey, SectionTitle: item.Identity.SectionTitle}
		}
		if summary.EligibleItemCount == math.MaxInt || len(item.Media) > math.MaxInt-summary.DeclaredMediaCount {
			return nil, fmt.Errorf("declared record total overflows int")
		}
		summary.EligibleItemCount++
		summary.DeclaredMediaCount += len(item.Media)
		for _, media := range item.Media {
			for _, part := range media.Parts {
				if part.DeclaredBytes == nil {
					summary.UnknownPartCount++
				} else {
					if *part.DeclaredBytes > math.MaxInt64-summary.KnownBytes {
						return nil, fmt.Errorf("declared byte total overflows int64")
					}
					summary.KnownPartCount++
					summary.KnownBytes += *part.DeclaredBytes
				}
			}
		}
		bySection[item.Identity.SectionKey] = summary
	}
	result := make([]StorageSummary, 0, len(bySection))
	for _, summary := range bySection {
		result = append(result, summary)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SectionKey < result[j].SectionKey })
	return result, nil
}

func validateItems(items []Item) error {
	sectionTitles := make(map[string]string)
	for itemIndex, item := range items {
		if strings.TrimSpace(item.Identity.SectionKey) == "" || strings.TrimSpace(item.Identity.RatingKey) == "" {
			return fmt.Errorf("item %d has incomplete identity", itemIndex)
		}
		if title, ok := sectionTitles[item.Identity.SectionKey]; ok && title != item.Identity.SectionTitle {
			return fmt.Errorf("item %d has conflicting section metadata", itemIndex)
		}
		sectionTitles[item.Identity.SectionKey] = item.Identity.SectionTitle
		for mediaIndex, media := range item.Media {
			for partIndex, part := range media.Parts {
				if !isSafePartReference(part.Reference) {
					return fmt.Errorf("item %d media %d part %d has invalid reference", itemIndex, mediaIndex, partIndex)
				}
				if part.DeclaredBytes != nil && *part.DeclaredBytes < 0 {
					return fmt.Errorf("item %d media %d part %d has negative declared bytes", itemIndex, mediaIndex, partIndex)
				}
				if part.Probe > ProbeFailed {
					return fmt.Errorf("item %d media %d part %d has invalid probe state", itemIndex, mediaIndex, partIndex)
				}
			}
		}
	}
	return nil
}

func isSafePartReference(reference string) bool {
	if reference == "" || strings.TrimSpace(reference) != reference {
		return false
	}
	for _, r := range reference {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	parsed, err := url.ParseRequestURI(reference)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.EscapedPath() != reference {
		return false
	}
	if !utf8.ValidString(parsed.Path) || pathpkg.Clean(parsed.Path) != parsed.Path {
		return false
	}
	for _, r := range parsed.Path {
		if unicode.IsControl(r) {
			return false
		}
	}
	segments := strings.Split(parsed.Path, "/")
	return len(segments) >= 5 && segments[0] == "" && segments[1] == "library" && segments[2] == "parts" && segments[3] != "" && segments[4] != ""
}

// Status classifies a safe integrity candidate.
type Status string

const (
	StatusProbeFailed Status = "probe_failed"
	StatusDuplicate   Status = "duplicate"
	StatusNoMedia     Status = "no_media"
	StatusNoParts     Status = "no_parts"
	StatusZeroBytes   Status = "zero_bytes"
)

// Candidate is a safe report record. Fingerprint is the first 16 lowercase hex characters of SHA-256(Reference).
type Candidate struct {
	Identity
	Fingerprint string
	Status      Status
}

func fingerprint(reference string) string {
	sum := sha256.Sum256([]byte(reference))
	return fmt.Sprintf("%x", sum[:8])
}

// UnavailableParts returns only parts whose caller-provided probe failed.
func UnavailableParts(items []Item) ([]Candidate, error) {
	if err := validateItems(items); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0)
	for _, item := range items {
		for _, media := range item.Media {
			for _, part := range media.Parts {
				if part.Probe == ProbeFailed {
					result = append(result, Candidate{Identity: item.Identity, Fingerprint: fingerprint(part.Reference), Status: StatusProbeFailed})
				}
			}
		}
	}
	sortCandidates(result)
	return result, nil
}

// DuplicateParts returns every part whose raw reference occurs more than once.
func DuplicateParts(items []Item) ([]Candidate, error) {
	if err := validateItems(items); err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, item := range items {
		for _, media := range item.Media {
			for _, part := range media.Parts {
				counts[part.Reference]++
			}
		}
	}
	result := make([]Candidate, 0)
	for _, item := range items {
		for _, media := range item.Media {
			for _, part := range media.Parts {
				if counts[part.Reference] > 1 {
					result = append(result, Candidate{Identity: item.Identity, Fingerprint: fingerprint(part.Reference), Status: StatusDuplicate})
				}
			}
		}
	}
	sortCandidates(result)
	return result, nil
}

// SuspiciousParts identifies items with no media and media records with no parts.
func SuspiciousParts(items []Item) ([]Candidate, error) {
	if err := validateItems(items); err != nil {
		return nil, err
	}
	result := make([]Candidate, 0)
	for _, item := range items {
		if len(item.Media) == 0 {
			result = append(result, Candidate{Identity: item.Identity, Status: StatusNoMedia})
			continue
		}
		for _, media := range item.Media {
			if len(media.Parts) == 0 {
				result = append(result, Candidate{Identity: item.Identity, Status: StatusNoParts})
				continue
			}
			for _, part := range media.Parts {
				if part.DeclaredBytes != nil && *part.DeclaredBytes == 0 {
					result = append(result, Candidate{Identity: item.Identity, Fingerprint: fingerprint(part.Reference), Status: StatusZeroBytes})
				}
			}
		}
	}
	sortCandidates(result)
	return result, nil
}
func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		for _, pair := range [][2]string{{candidates[i].SectionKey, candidates[j].SectionKey}, {candidates[i].RatingKey, candidates[j].RatingKey}, {candidates[i].Fingerprint, candidates[j].Fingerprint}, {string(candidates[i].Status), string(candidates[j].Status)}, {candidates[i].Title, candidates[j].Title}, {candidates[i].SectionTitle, candidates[j].SectionTitle}} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return false
	})
}
