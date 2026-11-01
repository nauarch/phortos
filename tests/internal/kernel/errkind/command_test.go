// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package errkind_test

// Verifies:
//   - A failed command names what ran and what it printed, and keeps its kind and cause
//   - A command that printed nothing leaves no empty gap

import (
	"errors"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAFailedCommandSaysWhatItPrinted(t *testing.T) {
	cause := errors.New("exit status 5")
	err := errkind.Command("systemctl restart docker", "  Unit docker.service not found.\n", errkind.ErrUpstream, cause)
	if err.Error() != "systemctl restart docker: Unit docker.service not found.: upstream failed: exit status 5" || errkind.Of(err) != errkind.Upstream || !errors.Is(err, cause) {
		t.Errorf("%v", err)
	}
	if got := errkind.Command("docker info", " \n", errkind.ErrUnreachable, cause).Error(); got != "docker info: unreachable: exit status 5" {
		t.Errorf("%s", got)
	}
}
