// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package allow_test

// Verifies:
//   - Exact names match in any case, and a "*." pattern matches subdomains but not the domain
//     itself
//   - A wildcard anywhere but at the start is invalid
//   - The patterns read back sorted, exact names first

import (
	"slices"
	"testing"

	"github.com/nauarch/phortos/internal/features/cache/domain/allow"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestHostsMatchTheirPatterns(t *testing.T) {
	l, err := allow.New([]string{"deb.debian.org", " *.archive.ubuntu.com", "127.0.0.1:8080", ""})
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"deb.debian.org":            true,
		"DEB.Debian.org":            true,
		"de.archive.ubuntu.com":     true,
		"archive.ubuntu.com":        false,
		"evil-archive.ubuntu.com":   false,
		"127.0.0.1:8080":            true,
		"127.0.0.1":                 false,
		"deb.debian.org.example.io": false,
	} {
		if got := l.Allows(host); got != want {
			t.Errorf("%s: %v", host, got)
		}
	}
	if got := l.Patterns(); !slices.Equal(got, []string{"127.0.0.1:8080", "deb.debian.org", "*.archive.ubuntu.com"}) {
		t.Error(got)
	}
}

func TestAMisplacedWildcardIsInvalid(t *testing.T) {
	for _, p := range []string{"deb.*.org", "*.", "*.a*.org", "*"} {
		if _, err := allow.New([]string{p}); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%s: %v", p, err)
		}
	}
}
