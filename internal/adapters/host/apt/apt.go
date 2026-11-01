// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package apt is the host's apt, run as its own programs: apt-config for the configuration,
// apt-get for updates. They run with LC_ALL=C, so their output reads the same on every host.
package apt

import (
	"context"

	"github.com/nauarch/phortos/internal/adapters/host/program"
	"github.com/nauarch/phortos/internal/contracts/host"
)

// Exec runs apt's programs.
type Exec struct{}

var _ host.Apt = Exec{}

// Config is `apt-config dump`.
func (Exec) Config(ctx context.Context) (string, error) {
	return program.Run(ctx, "apt-config", "dump")
}

// Update is `apt-get update`.
func (Exec) Update(ctx context.Context) (string, error) {
	return program.Run(ctx, "apt-get", "update")
}
