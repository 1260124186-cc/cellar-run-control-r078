package clock

import (
	"sync"
	"time"
)

// Clock isolates time-dependent behavior from the service workflows.
type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time {
	return time.Now().UTC()
}

// Fixed is useful for deterministic local diagnostics and future tests.
type Fixed struct {
	mu  sync.Mutex
	now time.Time
}

func NewFixed(now time.Time) *Fixed {
	return &Fixed{now: now.UTC()}
}

func (f *Fixed) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fixed) Advance(duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(duration)
}
