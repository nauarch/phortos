// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package apparmor_test

// Verifies:
//   - The block runs detect under a profile of its own, allowed the shell, phortosctl, the state
//     and the network, and nothing of the ESM job's
//   - Put in, it keeps what the local additions held, replaces a block of its own instead of
//     adding a second, and taken out leaves the additions as they were, even a block cut short

import (
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/features/hosts/domain/apparmor"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
)

func TestTheBlockAllowsDetectOnly(t *testing.T) {
	b := string(apparmor.Block(layout.Linux()))
	for _, line := range []string{
		"/usr/local/lib/phortos/detect Cx -> phortos_detect,",
		"profile phortos_detect flags=(attach_disconnected) {",
		"  /usr/local/lib/phortos/phortosctl mrix,",
		"  /etc/phortos/phortosctl.json r,",
		"  network inet stream,",
	} {
		if !strings.Contains(b, line+"\n") {
			t.Errorf("missing %q in\n%s", line, b)
		}
	}
	if strings.Contains(b, " ix,") || strings.Contains(b, "Ux") {
		t.Errorf("detect must not run unconfined or as the ESM job:\n%s", b)
	}
}

func TestTheBlockGoesInAndOutLeavingTheRest(t *testing.T) {
	l := layout.Linux()
	mine := "  # the admin's own rule\n  /srv/x r,"
	once := apparmor.With([]byte(mine), l)
	if !strings.HasPrefix(string(once), mine+"\n") || strings.Count(string(once), "profile phortos_detect") != 1 {
		t.Fatalf("\n%s", once)
	}
	if twice := apparmor.With(once, l); string(twice) != string(once) {
		t.Errorf("put in twice:\n%s", twice)
	}
	if back := apparmor.Without(once); string(back) != mine+"\n" {
		t.Errorf("taken out: %q", back)
	}
	if empty := apparmor.Without(apparmor.With(nil, l)); len(empty) != 0 {
		t.Errorf("an empty file came back as %q", empty)
	}
	cut := []byte(mine + "\n" + strings.Split(string(apparmor.Block(l)), "\n")[0] + "\n/usr/local/lib/phortos/detect Cx")
	if back := apparmor.Without(cut); string(back) != mine+"\n" {
		t.Errorf("a block cut short: %q", back)
	}
}
