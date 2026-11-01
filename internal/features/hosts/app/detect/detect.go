// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package detect answers apt's question, once per host per run: through phortos, or DIRECT. It
// runs as apt's unprivileged user, reads only the kept state, and asks phortos within a second,
// so a phortos that is gone costs apt at most that second per host.
package detect

import (
	"context"
	"time"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/route"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/clock"
)

// DefaultWithin is how long detect waits for phortos.
const DefaultWithin = time.Second

// Detector decides from the state at Layout.State and phortos' answer.
type Detector struct {
	Files   host.Files
	Phortos host.Phortos
	Layout  layout.Layout
	Clock   clock.Clock
	Within  time.Duration
}

// Detect is the answer for url: phortos' proxy address, or DIRECT on anything amiss.
func (d *Detector) Detect(ctx context.Context, url string) string {
	b, err := d.Files.Read(d.Layout.State)
	if err != nil {
		return ops.Direct
	}
	st, err := state.Decode(b)
	if err != nil {
		return ops.Direct
	}
	ctx, cancel := context.WithTimeout(ctx, d.Within)
	defer cancel()
	r, err := d.Phortos.Route(ctx, st.Server, url)
	return route.Decide(url, r, err == nil, st, d.Clock.Now())
}
