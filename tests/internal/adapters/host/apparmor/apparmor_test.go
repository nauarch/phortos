// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package apparmor_test

// Verifies:
//   - Reloading a profile that is not there fails as an upstream failure naming the command, on a
//     host with apparmor_parser or without it, not a panic

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/host/apparmor"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAMissingProfileFailsToReload(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-profile")
	err := apparmor.Exec{}.Reload(t.Context(), missing)
	if errkind.Of(err) != errkind.Upstream || !strings.Contains(err.Error(), "apparmor_parser -r "+missing) {
		t.Errorf("%v", err)
	}
}
