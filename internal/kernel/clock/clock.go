// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package clock

import "time"

// Clock tells the time, so tests can fix it.
type Clock interface {
	Now() time.Time
}

// System is the clock of the machine phortos runs on.
type System struct{}

// Now is the current time.
func (System) Now() time.Time { return time.Now() }
