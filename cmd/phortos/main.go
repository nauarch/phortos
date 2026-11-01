// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/nauarch/phortos/internal/adapters/config/yamlfile"
	"github.com/nauarch/phortos/internal/contracts/config"
	"github.com/nauarch/phortos/internal/kernel/build"
	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/internal/server/settings"
)

// version is the release, stamped at build time (-ldflags "-X main.version=v1.2.3").
var version = "dev"

// main reads the command line (and the config file it names) and runs phortos until stopped by a
// signal; --version prints the version, anywhere among the arguments, and --debug logs every
// answer. The config file is read again as phortos runs and on SIGHUP. install and uninstall set
// it up as (or remove it from being) a systemd service.
func main() {
	if slices.Contains(os.Args[1:], "--version") || len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(build.About("phortos", build.Version(version)))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "install" {
		os.Exit(install(os.Args[2:], os.Stdout))
	}
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		os.Exit(uninstall(os.Args[2:], os.Stdout))
	}
	args := os.Args[1:]
	c, err := settings.Load(args, openConfig, os.Stderr)
	if err != nil {
		if !errors.Is(err, settings.ErrUsage) {
			fmt.Fprintln(os.Stderr, "phortos:", err)
		}
		os.Exit(2)
	}
	level := slog.LevelInfo
	if c.Debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	o := identified(c.Options)
	o.Version = build.Version(version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o.ReloadNow = hangups(ctx)
	if c.Config != "" {
		log.Info("settings read", "config", c.Config)
		o.Reload = func() (server.Options, error) {
			c, err := settings.Load(args, openConfig, io.Discard)
			return identified(c.Options), err
		}
	}
	if err := server.Run(ctx, o, log); err != nil {
		log.Error("phortos stopped", "err", err)
		stop()
		os.Exit(1)
	}
}

// openConfig is the config file at path.
func openConfig(path string) config.File { return yamlfile.File{Path: path} }

// identified is o run by this process: its machine's name and its pid, holding the store's lock.
func identified(o server.Options) server.Options {
	host, _ := os.Hostname()
	o.Host, o.PID = host, os.Getpid()
	o.Owner = fmt.Sprintf("phortos on %s (pid %d)", host, o.PID)
	return o
}

// hangups fires for every SIGHUP (systemctl reload) until ctx ends, dropping one that comes while
// the last is still unread. Caught, a SIGHUP no longer ends phortos, with a config file or not.
func hangups(ctx context.Context) <-chan struct{} {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGHUP)
	now := make(chan struct{}, 1)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sig:
				select {
				case now <- struct{}{}:
				default:
				}
			}
		}
	}()
	return now
}
