// Package sessiondiagnostics provides pure, deterministic active-session analysis.
package sessiondiagnostics

import (
	"fmt"
	"sort"
	"strings"
)

// Decision describes the playback delivery method shown in a session report.
type Decision string

const (
	DecisionDirectPlay   Decision = "direct_play"
	DecisionDirectStream Decision = "direct_stream"
	DecisionTranscode    Decision = "transcode"
	DecisionUnknown      Decision = "unknown"
)

// Session is a normalized active-session record. Its identifiers are opaque.
type Session struct {
	SessionID, Title, GrandparentTitle, ParentTitle string
	UserID, UserTitle                               string
	ClientID, ClientTitle, ClientPlatform           string
	Decision                                        Decision
}

// Row is a safe, display-ready active-session record.
type Row struct {
	SessionID, Title, GrandparentTitle, ParentTitle string
	UserID, UserTitle                               string
	ClientID, ClientTitle, ClientPlatform           string
	Decision                                        Decision
}

// DecisionCount is an aggregate count for one delivery decision.
type DecisionCount struct {
	Decision Decision
	Count    int
}

// Report contains stable session rows and delivery decision aggregates.
type Report struct {
	Rows           []Row
	DecisionCounts []DecisionCount
}

// Analyze returns a deterministic safe session report.
func Analyze(sessions []Session) (Report, error) {
	rows := make([]Row, 0, len(sessions))
	counts := make(map[Decision]int)
	seen := make(map[string]struct{}, len(sessions))
	for index, session := range sessions {
		if strings.TrimSpace(session.SessionID) == "" {
			return Report{}, fmt.Errorf("session %d has blank session identity", index)
		}
		if _, exists := seen[session.SessionID]; exists {
			return Report{}, fmt.Errorf("session %d has duplicate session identity", index)
		}
		seen[session.SessionID] = struct{}{}
		decision := classify(session.Decision)
		rows = append(rows, Row{
			SessionID: session.SessionID, Title: session.Title, GrandparentTitle: session.GrandparentTitle, ParentTitle: session.ParentTitle,
			UserID: session.UserID, UserTitle: session.UserTitle, ClientID: session.ClientID, ClientTitle: session.ClientTitle, ClientPlatform: session.ClientPlatform,
			Decision: decision,
		})
		counts[decision]++
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SessionID < rows[j].SessionID })
	decisions := []Decision{DecisionDirectPlay, DecisionDirectStream, DecisionTranscode, DecisionUnknown}
	decisionCounts := make([]DecisionCount, 0, len(decisions))
	for _, decision := range decisions {
		if count := counts[decision]; count != 0 {
			decisionCounts = append(decisionCounts, DecisionCount{Decision: decision, Count: count})
		}
	}
	return Report{Rows: rows, DecisionCounts: decisionCounts}, nil
}

func classify(decision Decision) Decision {
	switch decision {
	case DecisionDirectPlay, DecisionDirectStream, DecisionTranscode:
		return decision
	default:
		return DecisionUnknown
	}
}

// DecisionFromMediaDecisions validates documented per-stream PMS decisions and
// returns their deterministic session-level delivery classification.
func DecisionFromMediaDecisions(video, audio, subtitles string) (Decision, error) {
	decisions := []string{video, audio, subtitles}
	for _, decision := range decisions {
		switch decision {
		case "directplay", "copy", "transcode":
		default:
			return DecisionUnknown, fmt.Errorf("invalid media decision")
		}
	}
	for _, decision := range decisions {
		if decision == "transcode" {
			return DecisionTranscode, nil
		}
	}
	for _, decision := range decisions {
		if decision == "copy" {
			return DecisionDirectStream, nil
		}
	}
	return DecisionDirectPlay, nil
}
