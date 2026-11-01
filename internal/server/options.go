// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/nauarch/phortos/internal/frontdoor/setup/dist"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// DefaultHosts are the repository hosts served when none are named: Debian (ftp.debian.org is
// what Proxmox installs write), Ubuntu (with its country mirrors and ports) and Proxmox'
// no-subscription repositories, all plain HTTP. phortos install writes them into a new config
// file, so there the list is complete and grows by adding to it; naming hosts anywhere replaces
// them.
var DefaultHosts = []string{
	"deb.debian.org", "ftp.debian.org", "security.debian.org",
	"archive.ubuntu.com", "*.archive.ubuntu.com", "security.ubuntu.com", "ports.ubuntu.com",
	"download.proxmox.com",
}

// Options are phortos' settings: where it listens and keeps its store, which hosts it serves,
// how it reaches them, its timing (zero takes the cache's defaults), the bytes a second below
// which a download is given up (0 for no floor), the store's cap in bytes (0 for none), who holds the store's lock, the version it reports, and (for tests) a function told
// the address once listening.
//
// Keys is the folder of phortos' identity and working CAs. With it, the API (the trust bundle
// clients install, and the setup script) is served over TLS pinned to the identity on APIAddr,
// told to APIListening. Intercept names the HTTPS hosts phortos ends TLS for, which need Keys;
// they are served too. Transport, when set, is how repositories are reached (tests pass their
// own); nil builds the real one.
//
// Tools are the phortosctl binaries the setup door hands out (nil for those built into phortos).
// Advertise is the name hosts reach phortos by, put in the one-liner phortos logs ("" for Host,
// the machine's name); the one-liner a host fetches names what that host asked for.
//
// SelfCheck is how often phortos asks its own /healthz (zero for 30s, negative never); Exit ends
// the process when stopping after unanswered checks hangs too (nil for os.Exit); Notify tells
// systemd phortos is ready and, after each answered check, alive (nil for Notify, which does
// nothing outside systemd).
//
// Reload, when set, reads the settings again (from the config file): phortos calls it every
// ReloadEvery (zero for DefaultReloadEvery, negative never) and whenever ReloadNow fires (a
// SIGHUP), and applies what changed (see reloader).
type Options struct {
	Transport     http.RoundTripper
	Addr          string
	Dir           string
	Init          bool
	Hosts         []string
	Intercept     []string
	Keys          string
	APIAddr       string
	Resolver      string
	ReleaseTTL    time.Duration
	ReleaseWithin time.Duration
	StallAfter    time.Duration
	MinRate       int64
	MaxBytes      int64
	EvictEvery    time.Duration
	Owner         string
	Host          string
	Advertise     string
	PID           int
	Version       string
	SelfCheck     time.Duration
	Exit          func(code int)
	Notify        func(state string) error
	Reload        func() (Options, error)
	ReloadEvery   time.Duration
	ReloadNow     <-chan struct{}
	Tools         fs.FS
	Listening     func(addr string)
	APIListening  func(addr string)
}

// Defaults fills what the options leave empty and refuses what cannot run: no store folder, a
// negative cap, or hosts to intercept without a key folder.
func Defaults(o Options) (Options, error) {
	if o.Dir == "" {
		return o, fmt.Errorf("no store folder given (--dir): %w", errkind.ErrInvalid)
	}
	if o.MaxBytes < 0 {
		return o, fmt.Errorf("a negative store cap: %w", errkind.ErrInvalid)
	}
	if o.SelfCheck == 0 {
		o.SelfCheck = 30 * time.Second
	}
	if o.Exit == nil {
		o.Exit = os.Exit
	}
	if o.Notify == nil {
		o.Notify = Notify
	}
	if o.Tools == nil {
		tools, err := fs.Sub(dist.Files, "bin")
		if err != nil {
			return o, err
		}
		o.Tools = tools
	}
	if o.Addr == "" {
		o.Addr = ":3142"
	}
	if len(o.Hosts) == 0 {
		o.Hosts = DefaultHosts
	}
	if len(o.Intercept) > 0 {
		if o.Keys == "" {
			return o, fmt.Errorf("hosts to intercept need a key folder (--keys): %w", errkind.ErrInvalid)
		}
		o.Hosts = append(slices.Clone(o.Hosts), o.Intercept...)
	}
	if o.Keys != "" && o.APIAddr == "" {
		o.APIAddr = ":3143"
	}
	if o.Owner == "" {
		o.Owner = "phortos"
	}
	if o.Version == "" {
		o.Version = "dev"
	}
	return o, nil
}
