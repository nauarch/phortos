// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package apparmor is the host's AppArmor, through apparmor_parser. Replacing a loaded profile is
// all or nothing: one that does not compile leaves the loaded one in place.
package apparmor

import (
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Exec runs apparmor_parser.
type Exec struct{}

var _ host.AppArmor = Exec{}

// Reload is `apparmor_parser -r <profile>`.
func (Exec) Reload(ctx context.Context, profile string) error {
	cmd := exec.CommandContext(ctx, "apparmor_parser", "-r", profile)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return errkind.Command("apparmor_parser -r "+profile, out.String(), errkind.ErrUpstream, err)
	}
	return nil
}
