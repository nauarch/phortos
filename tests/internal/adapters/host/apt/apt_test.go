// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package apt_test

// Verifies:
//   - On a host with apt, the configuration dump comes back whole
//   - On a host without apt, asking fails as an upstream failure naming the command, not a panic

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/host/apt"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestTheConfigurationIsDumped(t *testing.T) {
	dump, err := apt.Exec{}.Config(t.Context())
	if _, missing := exec.LookPath("apt-config"); missing != nil {
		if errkind.Of(err) != errkind.Upstream || !strings.Contains(err.Error(), "apt-config dump") {
			t.Errorf("no apt: %v", err)
		}
		return
	}
	if err != nil || !strings.Contains(dump, "APT::Architecture") {
		t.Errorf("%v %.200s", err, dump)
	}
}
