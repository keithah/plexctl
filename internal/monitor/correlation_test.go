package monitor

import (
	"testing"
	"time"
)

func TestCorrelationTrackerEmitsOneSafeEventForDistinctTargets(t *testing.T) {
	now := time.Unix(1000, 0)
	tracker := NewCorrelationTracker(5*time.Minute, 3, func() time.Time { return now })
	for _, target := range []string{"one", "two"} {
		if event := tracker.ObserveFailure(target); event != nil {
			t.Fatalf("early event for %q: %+v", target, event)
		}
	}
	event := tracker.ObserveFailure("three")
	if event == nil || event.TargetCount != 3 {
		t.Fatalf("event=%+v, want three-target event", event)
	}
	if event := tracker.ObserveFailure("four"); event != nil {
		t.Fatalf("repeat event=%+v, want nil", event)
	}
	if got := event.String(); got != "monitor dependency suspected: 3 targets" {
		t.Fatalf("event string=%q", got)
	}
}

func TestCorrelationTrackerExpiresOldFailures(t *testing.T) {
	now := time.Unix(1000, 0)
	tracker := NewCorrelationTracker(time.Minute, 2, func() time.Time { return now })
	if event := tracker.ObserveFailure("one"); event != nil {
		t.Fatalf("unexpected event=%+v", event)
	}
	now = now.Add(2 * time.Minute)
	if event := tracker.ObserveFailure("two"); event != nil {
		t.Fatalf("expired failure formed event=%+v", event)
	}
}
