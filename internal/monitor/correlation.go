package monitor

import (
	"fmt"
	"sync"
	"time"
)

// CorrelationEvent is deliberately aggregate-only. It contains no accounts,
// server identifiers, endpoints, or upstream response details.
type CorrelationEvent struct {
	TargetCount int
}

func (e CorrelationEvent) String() string {
	return fmt.Sprintf("monitor dependency suspected: %d targets", e.TargetCount)
}

// CorrelationTracker reports one aggregate event when distinct monitor targets
// fail in a bounded time window. It never changes individual monitor outcomes.
type CorrelationTracker struct {
	mu        sync.Mutex
	window    time.Duration
	threshold int
	now       func() time.Time
	failures  map[string]time.Time
	emitted   bool
}

func NewCorrelationTracker(window time.Duration, threshold int, now func() time.Time) *CorrelationTracker {
	if now == nil {
		now = time.Now
	}
	return &CorrelationTracker{
		window:    window,
		threshold: threshold,
		now:       now,
		failures:  make(map[string]time.Time),
	}
}

func (t *CorrelationTracker) ObserveFailure(target string) *CorrelationEvent {
	if t == nil || target == "" || t.window <= 0 || t.threshold < 2 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	for key, at := range t.failures {
		if now.Sub(at) > t.window {
			delete(t.failures, key)
		}
	}
	t.failures[target] = now
	if len(t.failures) < t.threshold {
		t.emitted = false
		return nil
	}
	if t.emitted {
		return nil
	}
	t.emitted = true
	return &CorrelationEvent{TargetCount: len(t.failures)}
}
