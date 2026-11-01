// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package hostplan is the contract between phortosctl's command line and the hosts feature: what
// pointing a host at phortos involves, and the commands that do it.
package hostplan

import (
	"context"
	"time"
)

// Target is the phortos a host is pointed at: its proxy (host:port), and, for intercepted HTTPS
// repositories, its API (host:port) and the pin of its identity. Without a pin no HTTPS
// repository goes through phortos. NoAutoUpgrade keeps refresh from installing the phortosctl
// phortos hands out.
type Target struct {
	Server        string
	API           string
	Pin           string
	NoAutoUpgrade bool
}

// CA is a working CA as a host installs it.
type CA struct {
	Fingerprint string
	NotAfter    time.Time
}

// Plan is what configure finds and would write.
type Plan struct {
	Target Target
	// Configured is true when phortosctl configured this host before.
	Configured bool
	// Intercepted are the HTTPS hosts phortos ends TLS for, with the working CAs installed for
	// them.
	Intercepted []string
	CAs         []CA
	// Credentialed are the hosts apt has credentials for: always sent straight.
	Credentialed []string
	// Conflicts are apt proxy settings of the host's own, which configure refuses to fight.
	Conflicts []string
	// Writes are the files configure writes.
	Writes []string
	// PhortosVersion is the version phortos was built as ("" when it could not be asked), and
	// Outdated is true when the phortosctl configure installs is not the one phortos hands out.
	PhortosVersion string
	Outdated       bool
}

// Report is what verify saw: the requests phortos answered during an apt update, and apt's
// warnings and errors.
type Report struct {
	Requests int64
	Warnings []string
}

// Manager points a host at phortos and keeps it so.
type Manager interface {
	// Plan reads the host and phortos and says what configure would do; it changes nothing.
	Plan(ctx context.Context, t Target) (Plan, error)
	// Configure installs phortosctl, its detect program, the CA bundle and the apt
	// configuration; it refuses a host whose own proxy settings conflict.
	Configure(ctx context.Context, t Target) (Plan, error)
	// Unconfigure removes everything configure wrote (only lists it unless yes), giving the paths.
	Unconfigure(ctx context.Context, yes bool) ([]string, error)
	// Refresh renews the CA bundle and the kept state from phortos, as apt's update hook does;
	// it says what changed.
	Refresh(ctx context.Context) (string, error)
	// Upgrade replaces the installed phortosctl with the one phortos hands out for this platform,
	// fetched pinned and checked against its checksum (only says what it would do unless yes).
	Upgrade(ctx context.Context, yes bool) (string, error)
	// Detect is what apt runs per host: phortos' proxy address for url, or DIRECT.
	Detect(ctx context.Context, url string) string
	// Verify runs an apt update and checks it went through phortos cleanly.
	Verify(ctx context.Context) (Report, error)
}
