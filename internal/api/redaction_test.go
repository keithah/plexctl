package api

import (
	"strings"
	"testing"
)

// Redaction must run before truncation. If the token straddles the truncation
// boundary, truncating first leaves an unredacted prefix in the error detail.
func TestSafeDetailRedactsTokenStraddlingTruncationBoundary(t *testing.T) {
	token := "SUPERSECRETTOKENVALUE1234567890"
	body := strings.Repeat("A", 290) + token + strings.Repeat("B", 50)

	got := safeDetail([]byte(body), token)

	for n := len(token); n >= 6; n-- {
		if strings.Contains(got, token[:n]) {
			t.Fatalf("%d-char token prefix %q survived redaction: %q", n, token[:n], got)
		}
	}
}

// Redaction must not depend on where the token appears in the body.
func TestSafeDetailRedactsTokenAnywhereInBody(t *testing.T) {
	token := "TOKENVALUE0987654321"
	for name, body := range map[string]string{
		"start":  token + strings.Repeat("A", 400),
		"middle": strings.Repeat("A", 150) + token + strings.Repeat("B", 400),
		"end":    strings.Repeat("A", 50) + token,
	} {
		if got := safeDetail([]byte(body), token); strings.Contains(got, token) {
			t.Errorf("%s: token survived redaction: %q", name, got)
		}
	}
}

// Truncation must still bound the detail after redaction runs.
func TestSafeDetailStillTruncatesLongBodies(t *testing.T) {
	got := safeDetail([]byte(strings.Repeat("A", 5000)), "")
	if len([]rune(got)) > 301 {
		t.Fatalf("detail not truncated: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated detail should be marked with an ellipsis: %q", got)
	}
}
