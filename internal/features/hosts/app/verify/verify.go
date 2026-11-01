// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package verify checks a configured host for real: it runs an apt update and looks at what
// phortos answered meanwhile and at apt's output. apt reports a failed TLS check or an
// unreachable repository only as a warning and still exits 0, so warnings fail the check too.
package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Verifier runs the check.
type Verifier struct {
	Files   host.Files
	Apt     host.Apt
	Phortos host.Phortos
	Layout  layout.Layout
}

// Verify runs `apt-get update` and fails when it failed, warned, or reached phortos not once.
func (v *Verifier) Verify(ctx context.Context) (hostplan.Report, error) {
	var r hostplan.Report
	b, err := v.Files.Read(v.Layout.State)
	if err != nil {
		return r, fmt.Errorf("this host is not configured for phortos (run phortosctl configure): %w", err)
	}
	st, err := state.Decode(b)
	if err != nil {
		return r, err
	}
	before, err := v.Phortos.Requests(ctx, st.Server)
	if err != nil {
		return r, fmt.Errorf("asking phortos at %s: %w", st.Server, err)
	}
	out, uerr := v.Apt.Update(ctx)
	after, err := v.Phortos.Requests(ctx, st.Server)
	if err != nil {
		return r, fmt.Errorf("asking phortos at %s: %w", st.Server, err)
	}
	r.Requests = after - before
	r.Warnings = Warnings(out)
	switch {
	case uerr != nil:
		return r, uerr
	case len(r.Warnings) > 0:
		return r, fmt.Errorf("apt warned during the update: %w", errkind.ErrUpstream)
	case r.Requests <= 0:
		return r, fmt.Errorf("the update did not reach phortos at %s: %w", st.Server, errkind.ErrUpstream)
	}
	return r, nil
}

// Warnings are apt's warning and error lines (W: and E:).
func Warnings(out string) []string {
	var w []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "W:") || strings.HasPrefix(line, "E:") {
			w = append(w, strings.TrimSpace(line))
		}
	}
	return w
}
