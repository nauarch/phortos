// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/adapters/config/yamlfile"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server/service"
	"github.com/nauarch/phortos/internal/server/settings"
)

// defaultDir, defaultKeys and defaultConfig are the store, keys folder and config file phortos
// install uses when --dir, --keys and --config are not given; --keys= (empty) installs without
// keys, so without the pinned API, and --config= without a config file, every setting then
// staying on the unit's command line.
const (
	defaultDir    = "/var/lib/phortos/store"
	defaultKeys   = "/var/lib/phortos/keys"
	defaultConfig = "/etc/phortos/phortos.yaml"
)

// configComment heads the config file install writes.
const configComment = `phortos' settings, written by phortos install.
Keys are phortos' flags without the dashes (phortos -h lists them).
hosts are the plain-HTTP repository hosts served, intercept the HTTPS ones;
add a repository by adding its host to the list.
hosts, intercept and max-size apply within seconds of a change, or at once
with systemctl reload phortos; other settings at the next restart.
The store and keys folders are set in the unit (systemctl cat phortos).`

// install installs phortos as a systemd service serving with the settings given (as phortos
// takes them, each as --name=value), which it writes to a new config file; uninstall removes the
// service. Both print their steps, and take them only with --yes.
func install(args []string, out io.Writer) int {
	yes := slices.Contains(args, "--yes")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--yes" || a == "--init" })
	for _, d := range [][2]string{{"--keys", defaultKeys}, {"--dir", defaultDir}, {"--config", defaultConfig}} {
		if !given(args, d[0]) {
			args = append([]string{d[0] + "=" + d[1]}, args...)
		}
	}
	c, err := settings.Parse(args, os.Stderr)
	if err != nil {
		return 2
	}
	unitArgs, inFile := args, []string(nil)
	if c.Config != "" {
		if unitArgs, inFile, err = settings.Split(args); err != nil {
			fmt.Fprintln(os.Stderr, "phortos install:", err)
			return 2
		}
	}
	_, statErr := os.Stat(c.Config)
	exists := c.Config != "" && statErr == nil
	given := len(inFile) > 0
	if c.Config != "" && !exists {
		inFile = settings.WithDefaults(inFile)
	}
	body, err := yamlfile.Codec{}.Render(inFile, configComment)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phortos install:", err)
		return 2
	}
	serves := c
	if exists {
		if serves, err = settings.Load(unitArgs, openConfig, io.Discard); err != nil {
			fmt.Fprintln(os.Stderr, "phortos install:", err)
			return 1
		}
	}
	in, err := installer(serves.Options.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "phortos install:", err)
		return 1
	}
	steps, err := in.Install(context.Background(), service.Spec{
		Dir: c.Options.Dir, Keys: c.Options.Keys, Args: unitArgs,
		Config: service.Config{Path: c.Config, Body: body, Given: given},
	})
	return take(steps, err, yes, "install", out)
}

// uninstall removes the service phortos install made.
func uninstall(args []string, out io.Writer) int {
	in, err := installer("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "phortos uninstall:", err)
		return 1
	}
	steps, err := in.Uninstall(context.Background())
	if err == nil && len(steps) == 0 {
		fmt.Fprintln(out, "phortos is not installed as a service here: nothing to do.")
		return 0
	}
	return take(steps, err, slices.Contains(args, "--yes"), "uninstall", out)
}

// given reports whether a flag is among the arguments, as --flag or --flag=value.
func given(args []string, flag string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return a == flag || strings.HasPrefix(a, flag+"=") })
}

// take prints the steps and, with yes, takes them.
func take(steps []service.Step, err error, yes bool, what string, out io.Writer) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "phortos %s: %v\n", what, err)
		return 1
	}
	for i, s := range steps {
		fmt.Fprintf(out, "%d. %s\n", i+1, s.Say)
	}
	if !yes {
		fmt.Fprintf(out, "\nDry run: nothing was changed. Run again with --yes to %s.\n", what)
		return 0
	}
	fmt.Fprintln(out)
	for _, s := range steps {
		first, _, _ := strings.Cut(s.Say, "\n")
		fmt.Fprintf(out, "-> %s\n", first)
		if err := s.Do(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "phortos %s: %s: %v\n", what, first, err)
			return 1
		}
	}
	fmt.Fprintln(out, "\nDone.")
	return 0
}

// installer is the installer for this host, checking phortos' health at addr (":3142" when "").
func installer(addr string) (service.Installer, error) {
	self, err := os.Executable()
	if err != nil {
		return service.Installer{}, err
	}
	port := "3142"
	if i := strings.LastIndex(addr, ":"); i >= 0 && addr[i+1:] != "" {
		port = addr[i+1:]
	}
	url := "http://127.0.0.1:" + port + "/healthz"
	return service.Installer{Root: "/", Self: self, Run: run, Healthy: func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s answered %s", url, resp.Status)
		}
		return nil
	}}, nil
}

// run runs a command; a failure carries what it printed.
func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, errkind.Command(strings.Join(append([]string{name}, args...), " "), string(out), errkind.ErrUpstream, err)
	}
	return out, nil
}
