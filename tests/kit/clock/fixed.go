// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package clock

import (
	"sync"
	"time"
)

// Fixed is a clock stopped at one moment, moved on only by Advance.
type Fixed struct {
	mu sync.Mutex
	at time.Time
}

// At is a clock stopped at t.
func At(t time.Time) *Fixed { return &Fixed{at: t} }

// Now is the moment the clock is stopped at.
func (f *Fixed) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

// Advance moves the clock on.
func (f *Fixed) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
}
