// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package host is the contract of what phortosctl works with on a host: its files, its apt, its
// programs, its AppArmor, and the phortos it points apt at.
package host

import (
	"context"
	"crypto/ecdsa"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/trust"
)

// Files are the host's files, by absolute path. Missing ones are errkind.ErrNotFound.
type Files interface {
	Read(path string) ([]byte, error)
	// Write replaces a file whole (through a temporary file and a rename), making its folder.
	Write(path string, data []byte, perm uint32) error
	// List names a folder's entries.
	List(dir string) ([]string, error)
	// Remove removes a file or a folder with all it contains; a missing one is no error.
	Remove(path string) error
}

// Apt is the host's apt.
type Apt interface {
	// Config is apt's whole configuration, as `apt-config dump` prints it.
	Config(ctx context.Context) (string, error)
	// Update runs `apt-get update` and gives what it printed; an exit code other than 0 is an
	// error carrying the output.
	Update(ctx context.Context) (string, error)
}

// Programs runs programs on the host.
type Programs interface {
	// Run runs the program at path with args and gives what it printed; an exit code other than 0
	// is an error carrying the output.
	Run(ctx context.Context, path string, args ...string) (string, error)
}

// AppArmor is the host's AppArmor.
type AppArmor interface {
	// Reload loads a profile file again, after its local additions changed; a profile that does
	// not compile is an error carrying why, and the one loaded before stays.
	Reload(ctx context.Context, profile string) error
}

// Phortos is the phortos a host is pointed at.
type Phortos interface {
	// Trust fetches the bundle from the API at api (host:port) over TLS whose key must match pin;
	// any other key ends the connection before a byte is read. It gives the identity's public
	// key as presented, for the bundle's signatures to be checked against.
	Trust(ctx context.Context, api, pin string) (trust.Bundle, *ecdsa.PublicKey, error)
	// Tools fetches, pinned the same way, the version phortos was built as and the checksums of
	// the phortosctl binaries it hands out.
	Tools(ctx context.Context, api, pin string) (ops.Tools, error)
	// Binary fetches, pinned the same way, one phortosctl binary by name.
	Binary(ctx context.Context, api, pin, name string) ([]byte, error)
	// Route asks the proxy at server (host:port) how to reach url.
	Route(ctx context.Context, server, url string) (ops.Route, error)
	// Requests is how many file and release requests the proxy at server has answered.
	Requests(ctx context.Context, server string) (int64, error)
}
