// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package trust is phortos' certificate authority for the HTTPS repositories it intercepts: a
// pinned identity, short-lived working CAs only apt trusts and only for the intercepted hosts,
// and the certificates phortos presents for them.
package trust

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/keys"
	contract "github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/trust/app/authority"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// The defaults of the certificates' lives.
const (
	// DefaultCALifetime is how long a working CA lives; its successor is made at half of it.
	DefaultCALifetime = 30 * 24 * time.Hour
	// DefaultLeafLifetime is how long a certificate for an intercepted host lives.
	DefaultLeafLifetime = 24 * time.Hour
	// rotateEvery is how often the CAs are looked at.
	rotateEvery = time.Hour
)

// Options are what the authority is built from: where its keys live, the hosts phortos ends TLS
// for (plain names, no wildcards; none still gives the identity, for the pinned API and setup, but
// no working CAs), the lifetimes (zero takes the defaults), the clock and the log.
type Options struct {
	Keys         keys.Store
	Hosts        []string
	CALifetime   time.Duration
	LeafLifetime time.Duration
	Clock        clock.Clock
	Log          *slog.Logger
}

// Trust is the authority: a contract.Issuer for the proxy door and a contract.Publisher for the
// API door.
type Trust struct {
	*authority.Authority
}

var (
	_ contract.Issuer    = (*Trust)(nil)
	_ contract.Publisher = (*Trust)(nil)
)

// Compose builds and starts the authority: the identity and working CAs are loaded or made now.
func Compose(o Options) (*Trust, error) {
	if o.Keys == nil {
		return nil, fmt.Errorf("trust: a key store is required: %w", errkind.ErrInvalid)
	}
	hosts, err := plain(o.Hosts)
	if err != nil {
		return nil, err
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.CALifetime <= 0 {
		o.CALifetime = DefaultCALifetime
	}
	if o.LeafLifetime <= 0 {
		o.LeafLifetime = DefaultLeafLifetime
	}
	a := &authority.Authority{Keys: o.Keys, Hosts: hosts, CALifetime: o.CALifetime, LeafLifetime: o.LeafLifetime, Clock: o.Clock, Log: o.Log}
	if err := a.Start(); err != nil {
		return nil, err
	}
	return &Trust{Authority: a}, nil
}

// SetHosts changes the intercepted hosts while phortos runs (see authority.SetHosts).
func (t *Trust) SetHosts(hosts []string) error {
	clean, err := plain(hosts)
	if err != nil {
		return err
	}
	return t.Authority.SetHosts(clean)
}

// plain is a host list lower case, sorted and without repeats, refusing what is not a plain
// host name.
func plain(hosts []string) ([]string, error) {
	var out []string
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, "*:/") {
			return nil, fmt.Errorf("trust: intercepted host %q: a plain host name is needed: %w", h, errkind.ErrInvalid)
		}
		out = append(out, h)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Maintain rotates the working CAs when due, until ctx ends.
func (t *Trust) Maintain(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(rotateEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := t.Rotate(); err != nil {
				log.Error("working CAs not rotated", "err", err)
			}
		}
	}
}
