// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package settings is phortos' command line: its flags, the config file they may name (its
// settings keyed by the same flags, the command line winning over it), and how install splits
// settings between the service's command line and the config file.
package settings

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/config"
	"github.com/nauarch/phortos/internal/features/cache"
	"github.com/nauarch/phortos/internal/kernel/bytesize"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server"
)

// ErrUsage marks a command line the flag parser refused; it has said why already.
var ErrUsage = errors.New("usage")

// Command is what a command line says: phortos' options (without who runs them: Owner, Host and
// PID are the caller's), whether to log every answer, and the config file it names.
type Command struct {
	Options server.Options
	Debug   bool
	Config  string
	set     map[string]bool
}

// Parse reads arguments, the flag parser writing what it refuses to out.
func Parse(args []string, out io.Writer) (Command, error) {
	var c Command
	o := &c.Options
	set := flag.NewFlagSet("phortos", flag.ContinueOnError)
	set.SetOutput(out)
	set.BoolVar(&c.Debug, "debug", false, "log every answer with where it came from (hit, miss, joined, passed, …)")
	set.StringVar(&c.Config, "config", "", "a YAML file of settings, keyed by these flags' names; flags given on the command line win over it")
	set.DurationVar(&o.ReloadEvery, "reload-every", server.DefaultReloadEvery, "how often the config file is read again: hosts, intercept and max-size apply at once, other settings at the next restart (negative: only on SIGHUP)")
	set.StringVar(&o.Addr, "addr", ":3142", "the address to listen on; port 0 picks a free one")
	set.StringVar(&o.Dir, "dir", "", "the store's folder, on a local disk or a mounted share (required)")
	set.BoolVar(&o.Init, "init", false, "make the store's folder and its marker file when they are missing")
	set.StringVar(&o.Resolver, "resolver", "", "a DNS server (host:port) to look up repositories with, instead of the system's")
	set.Func("max-size", "the store's cap, such as 200G or 1.5T; least recently used files are evicted beyond it (default none)", func(s string) error {
		n, err := bytesize.Parse(s)
		o.MaxBytes = n
		return err
	})
	set.DurationVar(&o.ReleaseTTL, "release-ttl", cache.DefaultReleaseTTL, "how long a fetched release file is answered without asking upstream; negative asks every time")
	set.DurationVar(&o.ReleaseWithin, "release-within", cache.DefaultReleaseWithin, "how long a release file's revalidation may take before the kept copy is served; negative waits for upstream")
	set.DurationVar(&o.StallAfter, "stall-after", cache.DefaultStallAfter, "how long a download may receive nothing before it is given up")
	o.MinRate = cache.DefaultMinRate
	set.Func("min-rate", "the bytes a second, such as 32K, below which a download is given up when it keeps that pace for a whole --stall-after; its connection closes, so the next try opens a fresh one (0 for no floor; default 32K)", func(s string) error {
		n, err := bytesize.Parse(s)
		o.MinRate = n
		return err
	})
	set.DurationVar(&o.EvictEvery, "evict-every", cache.DefaultEvictEvery, "how often the store is measured and trimmed to its cap")
	set.DurationVar(&o.SelfCheck, "self-check", 30*time.Second, "how often phortos asks its own /healthz, exiting after 3 unanswered in a row so its service manager restarts it (negative never)")
	set.Func("hosts", "comma-separated repository hosts to serve; *.domain serves its subdomains (default "+strings.Join(server.DefaultHosts, ",")+")", list(&o.Hosts))
	set.Func("intercept", "comma-separated HTTPS repository hosts to end TLS for and serve (plain names); needs --keys", list(&o.Intercept))
	set.StringVar(&o.Keys, "keys", "", "the folder of phortos' identity and working CAs, kept private; with it phortos serves its pinned API and the setup one-liner (needed with --intercept)")
	set.StringVar(&o.APIAddr, "api-addr", ":3143", "the address the pinned API (trust bundle, setup) listens on, with --keys")
	set.StringVar(&o.Advertise, "advertise", "", "the name hosts reach phortos by, for the setup one-liner phortos logs (default this machine's hostname)")
	if err := set.Parse(args); err != nil {
		return c, err
	}
	c.set = map[string]bool{}
	set.Visit(func(f *flag.Flag) { c.set[f.Name] = true })
	return c, nil
}

// Load reads a command line and the config file it names (opened with file): the file's settings
// first, then the command line's, which win over the file's for every flag the command line sets.
// A command line the flag parser refuses is ErrUsage.
func Load(args []string, file func(path string) config.File, out io.Writer) (Command, error) {
	c, err := Parse(args, out)
	if err != nil {
		return c, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if c.Config == "" {
		return c, nil
	}
	fromFile, err := file(c.Config).Args()
	if err != nil {
		return c, err
	}
	var merged []string
	for _, a := range fromFile {
		if !c.set[name(a)] {
			merged = append(merged, a)
		}
	}
	m, err := Parse(append(merged, args...), io.Discard)
	if err != nil {
		return c, fmt.Errorf("config file %s: %w", c.Config, err)
	}
	return m, nil
}

// placement are the flags install keeps on the service's command line, where phortos' config,
// store and keys are; every other setting goes in the config file.
var placement = map[string]bool{"config": true, "dir": true, "keys": true}

// lists are the settings that take several values, written to the config file one per item.
var lists = map[string]bool{"hosts": true, "intercept": true}

// Split divides install's settings (each --name=value) between the service's command line (where
// the config, store and keys are) and the config file (the rest, a list's values one by one).
func Split(args []string) (unit, file []string, err error) {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			return nil, nil, fmt.Errorf("give settings as --name=value, not %q: %w", a, errkind.ErrInvalid)
		}
		n := name(a)
		_, value, _ := strings.Cut(a, "=")
		switch {
		case placement[n]:
			unit = append(unit, a)
		case lists[n]:
			for item := range strings.SplitSeq(value, ",") {
				if item = strings.TrimSpace(item); item != "" {
					file = append(file, "--"+n+"="+item)
				}
			}
		default:
			file = append(file, a)
		}
	}
	return unit, file, nil
}

// WithDefaults is a new config file's settings with the repository hosts phortos serves by
// default (server.DefaultHosts) written out one by one when the settings name none, so the file
// lists every plain-HTTP host served and one more is added by adding its line.
func WithDefaults(file []string) []string {
	if slices.ContainsFunc(file, func(a string) bool { return name(a) == "hosts" }) {
		return file
	}
	out := slices.Clone(file)
	for _, h := range server.DefaultHosts {
		out = append(out, "--hosts="+h)
	}
	return out
}

// name is a --name=value argument's flag name.
func name(arg string) string {
	n, _, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
	return n
}

// list is a flag adding comma-separated names to *to.
func list(to *[]string) func(string) error {
	return func(s string) error {
		for h := range strings.SplitSeq(s, ",") {
			if h = strings.TrimSpace(h); h != "" {
				*to = append(*to, h)
			}
		}
		return nil
	}
}
