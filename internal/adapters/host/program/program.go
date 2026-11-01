// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package program runs the host's programs, with LC_ALL=C, so their output reads the same on
// every host.
package program

import (
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// said is how much of a failing program's output its error carries, from the end.
const said = 4 << 10

// Exec runs programs.
type Exec struct{}

var _ host.Programs = Exec{}

// Run runs the program at path (or found on PATH) with args.
func (Exec) Run(ctx context.Context, path string, args ...string) (string, error) {
	return Run(ctx, path, args...)
}

// Run runs a program and gives everything it printed; a failure carries the end of it, and is
// named by the program and its first argument.
func Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		tail := out.Bytes()
		if len(tail) > said {
			tail = tail[len(tail)-said:]
		}
		label := name
		if len(args) > 0 {
			label += " " + args[0]
		}
		return out.String(), errkind.Command(label, string(tail), errkind.ErrUpstream, err)
	}
	return out.String(), nil
}
