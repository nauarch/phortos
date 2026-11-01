// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package clock_test

// Verifies:
//   - The system clock tells the machine's current time

import (
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/kernel/clock"
)

func TestTheSystemClockTellsTheCurrentTime(t *testing.T) {
	before := time.Now()
	got := clock.System{}.Now()
	if got.Before(before) || got.After(time.Now()) {
		t.Fatalf("%v is not now", got)
	}
}
