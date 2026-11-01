// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nauarch/phortos/internal/features/cache"
	"github.com/nauarch/phortos/internal/features/trust"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// DefaultReloadEvery is how often the settings are read again to see whether they changed.
const DefaultReloadEvery = 5 * time.Second

// reloader applies what the settings say now to the running phortos: the hosts served, the
// hosts intercepted (a new working CA) and the store's cap. Any other setting that changed is
// logged as waiting for a restart, once.
type reloader struct {
	running Options
	cache   *cache.Cache
	trust   *trust.Trust
	log     *slog.Logger
	failed  string
}

// run reads the settings with load every interval (negative never) and whenever now fires, until
// ctx ends.
func (r *reloader) run(ctx context.Context, load func() (Options, error), every time.Duration, now <-chan struct{}) {
	var tick <-chan time.Time
	if every > 0 {
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			r.once(load, false)
		case <-now:
			r.once(load, true)
		}
	}
}

// once reads and applies the settings. A failure keeps what runs and is logged once until it
// changes; asked for (a signal), the outcome is always logged.
func (r *reloader) once(load func() (Options, error), asked bool) {
	n, err := load()
	if err == nil {
		err = r.apply(n)
	}
	if err != nil {
		if msg := err.Error(); asked || msg != r.failed {
			r.failed = msg
			r.log.Error("settings not reloaded; phortos keeps running as it was", "err", err)
		}
		return
	}
	r.failed = ""
	if asked {
		r.log.Info("settings reloaded")
	}
}

// apply makes the running phortos what n says, as far as it can without a restart.
func (r *reloader) apply(n Options) error {
	n, err := Defaults(n)
	if err != nil {
		return err
	}
	if len(n.Intercept) > 0 && r.trust == nil {
		return fmt.Errorf("intercepting needs the key folder phortos was started with (--keys); restart it with one: %w", errkind.ErrInvalid)
	}
	var changed []string
	if !sameSet(n.Hosts, r.running.Hosts) {
		if err := r.cache.SetHosts(n.Hosts); err != nil {
			return err
		}
		r.running.Hosts = n.Hosts
		changed = append(changed, "hosts")
	}
	if r.trust != nil && !sameSet(n.Intercept, r.running.Intercept) {
		if err := r.trust.SetHosts(n.Intercept); err != nil {
			return err
		}
		r.running.Intercept = n.Intercept
		changed = append(changed, "intercept")
	}
	if n.MaxBytes != r.running.MaxBytes {
		if err := r.cache.SetMax(n.MaxBytes); err != nil {
			return err
		}
		r.running.MaxBytes = n.MaxBytes
		changed = append(changed, "max-size")
	}
	if len(changed) > 0 {
		r.log.Info("settings changed", "changed", changed, "hosts", r.cache.Hosts(), "intercepted", r.running.Intercept, "cap", capOf(r.running.MaxBytes))
	}
	if later := restartOnly(r.running, n); len(later) > 0 {
		r.log.Warn("settings changed that take effect only when phortos restarts", "settings", later)
		r.running = keepRestartOnly(r.running, n)
	}
	return nil
}

// restartOnly names the settings that differ between what runs and n and need a restart.
func restartOnly(running, n Options) []string {
	var names []string
	for _, s := range []struct {
		name string
		same bool
	}{
		{"addr", running.Addr == n.Addr}, {"dir", running.Dir == n.Dir}, {"keys", running.Keys == n.Keys},
		{"api-addr", running.APIAddr == n.APIAddr}, {"resolver", running.Resolver == n.Resolver},
		{"release-ttl", running.ReleaseTTL == n.ReleaseTTL}, {"release-within", running.ReleaseWithin == n.ReleaseWithin},
		{"stall-after", running.StallAfter == n.StallAfter}, {"min-rate", running.MinRate == n.MinRate},
		{"evict-every", running.EvictEvery == n.EvictEvery},
		{"self-check", running.SelfCheck == n.SelfCheck}, {"advertise", running.Advertise == n.Advertise},
	} {
		if !s.same {
			names = append(names, s.name)
		}
	}
	return names
}

// keepRestartOnly records n's restart-only settings as seen, so they are reported once.
func keepRestartOnly(running, n Options) Options {
	running.Addr, running.Dir, running.Keys, running.APIAddr, running.Resolver = n.Addr, n.Dir, n.Keys, n.APIAddr, n.Resolver
	running.ReleaseTTL, running.ReleaseWithin, running.StallAfter, running.EvictEvery = n.ReleaseTTL, n.ReleaseWithin, n.StallAfter, n.EvictEvery
	running.SelfCheck, running.Advertise, running.MinRate = n.SelfCheck, n.Advertise, n.MinRate
	return running
}

// sameSet reports whether two lists hold the same names, in any order.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}
