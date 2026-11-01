// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package rotation decides when phortos' working CAs change. A CA lives for a lifetime; once half
// of it has passed, the next one is made, so clients install it alongside the current one well
// before it is needed. Near its end the current CA gives way to the next. A CA made for other
// hosts than the ones intercepted now is never used.
package rotation

import (
	"slices"
	"time"
)

// Margin is how close to its end a CA is replaced, so no certificate it issued outlives it by
// surprise.
const Margin = 24 * time.Hour

// CA is what rotation needs to know of a working CA.
type CA struct {
	NotAfter time.Time
	Hosts    []string
}

// Step is what to do.
type Step int

// The steps, in the order they apply.
const (
	// Promote: the next CA becomes the current one.
	Promote Step = iota
	// NewCurrent: make a current CA, dropping any next one.
	NewCurrent
	// NewNext: make the next CA.
	NewNext
)

// Plan is what to do now, given the current and next CAs (nil when there is none), the hosts
// intercepted (sorted) and the lifetime of a CA.
func Plan(current, next *CA, hosts []string, now time.Time, lifetime time.Duration) []Step {
	usable := func(c *CA) bool {
		return c != nil && slices.Equal(c.Hosts, hosts) && now.Before(c.NotAfter.Add(-Margin))
	}
	var steps []Step
	end := now.Add(lifetime)
	switch {
	case usable(current):
		end = current.NotAfter
	case usable(next):
		steps = append(steps, Promote)
		end, next = next.NotAfter, nil
	default:
		steps = append(steps, NewCurrent)
		next = nil
	}
	if end.Sub(now) < lifetime/2 && !usable(next) {
		steps = append(steps, NewNext)
	}
	return steps
}
