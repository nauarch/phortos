// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package ctl is phortosctl's composition root: the local host's files and apt, the phortos
// client, the hosts feature and the command line.
package ctl

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"

	"github.com/nauarch/phortos/internal/adapters/host/apparmor"
	"github.com/nauarch/phortos/internal/adapters/host/apt"
	"github.com/nauarch/phortos/internal/adapters/host/files"
	"github.com/nauarch/phortos/internal/adapters/host/program"
	"github.com/nauarch/phortos/internal/adapters/phortos/client"
	"github.com/nauarch/phortos/internal/features/hosts"
	"github.com/nauarch/phortos/internal/frontdoor/cli"
)

// Run composes phortosctl for the host it runs on and runs one command, giving its exit code;
// version is the build's.
func Run(ctx context.Context, version string, args []string, out, errOut io.Writer) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintln(errOut, "phortosctl: finding itself:", err)
		return 1
	}
	h, err := hosts.Compose(hosts.Options{
		Files:    files.Dir{Root: "/"},
		Apt:      apt.Exec{},
		Programs: program.Exec{},
		AppArmor: apparmor.Exec{},
		Phortos:  client.New(0),
		Layout:   hosts.LinuxLayout(),
		Self:     self,
		Platform: runtime.GOOS + "-" + runtime.GOARCH,
		Log:      slog.New(slog.NewTextHandler(errOut, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		fmt.Fprintln(errOut, "phortosctl:", err)
		return 1
	}
	return cli.Run(ctx, args, cli.Options{Manager: h, Version: version, Out: out, Err: errOut})
}
