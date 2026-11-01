// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package program_test

// Verifies:
//   - A program's output comes back whole, from stdout and stderr, with LC_ALL=C set
//   - One that fails, or is not there, is an upstream failure naming the program and its first
//     argument and carrying what it printed

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/host/program"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAProgramsOutputComesBack(t *testing.T) {
	out, err := program.Exec{}.Run(t.Context(), "/bin/sh", "-c", `echo "out $LC_ALL"; echo err >&2`)
	if err != nil || out != "out C\nerr\n" {
		t.Errorf("%q %v", out, err)
	}
}

func TestAFailingProgramSaysWhy(t *testing.T) {
	_, err := program.Exec{}.Run(t.Context(), "/bin/sh", "-c", "echo boom; exit 3")
	if errkind.Of(err) != errkind.Upstream || !strings.Contains(err.Error(), "/bin/sh -c") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("%v", err)
	}
	missing := filepath.Join(t.TempDir(), "no-such-program")
	if _, err := (program.Exec{}).Run(t.Context(), missing, "version"); errkind.Of(err) != errkind.Upstream || !strings.Contains(err.Error(), missing+" version") {
		t.Errorf("%v", err)
	}
}
