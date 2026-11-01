// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/nauarch/phortos/internal/ctl"
	"github.com/nauarch/phortos/internal/kernel/build"
)

// version is the release, stamped at build time (-ldflags "-X main.version=v1.2.3").
var version = "dev"

// main runs one phortosctl command, stopping it on a signal.
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := ctl.Run(ctx, build.Version(version), os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
