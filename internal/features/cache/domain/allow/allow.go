// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package allow decides which repository hosts phortos fetches from: an exact name, or every
// subdomain of one ("*.archive.ubuntu.com"). Anything else is refused, so phortos is never an
// open proxy.
package allow

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// List is a set of host patterns.
type List struct {
	exact    map[string]bool
	suffixes []string
}

// New reads patterns: host names, with a port when not the default, or "*." and a domain. A
// pattern with a wildcard anywhere else is invalid.
func New(patterns []string) (List, error) {
	l := List{exact: map[string]bool{}}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(p, "*."); ok {
			if rest == "" || strings.Contains(rest, "*") {
				return List{}, fmt.Errorf("host pattern %q: %w", p, errkind.ErrInvalid)
			}
			l.suffixes = append(l.suffixes, "."+rest)
			continue
		}
		if strings.Contains(p, "*") {
			return List{}, fmt.Errorf("host pattern %q: only a leading *. is allowed: %w", p, errkind.ErrInvalid)
		}
		l.exact[p] = true
	}
	return l, nil
}

// Allows reports whether host matches a pattern.
func (l List) Allows(host string) bool {
	host = strings.ToLower(host)
	if l.exact[host] {
		return true
	}
	for _, s := range l.suffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return true
		}
	}
	return false
}

// Patterns are the patterns, exact names first and sorted.
func (l List) Patterns() []string {
	out := make([]string, 0, len(l.exact)+len(l.suffixes))
	for h := range l.exact {
		out = append(out, h)
	}
	slices.Sort(out)
	for _, s := range l.suffixes {
		out = append(out, "*"+s)
	}
	return out
}
