// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package hosts is phortosctl's work on a host: pointing apt at phortos (through a detect
// program apt asks per host, so a phortos that is gone means apt goes straight), installing and
// renewing the working CAs for intercepted HTTPS repositories, and taking it all off again.
package hosts

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/features/hosts/app/detect"
	"github.com/nauarch/phortos/internal/features/hosts/app/install"
	"github.com/nauarch/phortos/internal/features/hosts/app/verify"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Layout is where phortosctl keeps its files on a host.
type Layout = layout.Layout

// LinuxLayout is the layout on a Debian or Ubuntu host.
func LinuxLayout() Layout { return layout.Linux() }

// Options are what the feature works with: the host's files, apt, programs (to try a new
// phortosctl before it is installed) and AppArmor (used only on a host with Ubuntu Pro's ESM
// profile), phortos, the layout, the path of the running phortosctl
// (installed from) and the platform it was built for
// (linux-amd64; "" never matches phortos' binaries, so phortosctl is never called outdated), how
// long detect waits (zero for a second), the clock and the log.
type Options struct {
	Files        host.Files
	Apt          host.Apt
	Programs     host.Programs
	AppArmor     host.AppArmor
	Phortos      host.Phortos
	Layout       Layout
	Self         string
	Platform     string
	DetectWithin time.Duration
	Clock        clock.Clock
	Log          *slog.Logger
}

// Hosts is the feature: a hostplan.Manager.
type Hosts struct {
	*install.Installer
	detector *detect.Detector
	verifier *verify.Verifier
}

var _ hostplan.Manager = (*Hosts)(nil)

// Compose builds the feature. Files, apt and phortos are required.
func Compose(o Options) (*Hosts, error) {
	if o.Files == nil || o.Apt == nil || o.Phortos == nil {
		return nil, fmt.Errorf("hosts: files, apt and phortos are required: %w", errkind.ErrInvalid)
	}
	if o.Layout == (Layout{}) {
		o.Layout = layout.Linux()
	}
	if o.DetectWithin <= 0 {
		o.DetectWithin = detect.DefaultWithin
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Hosts{
		Installer: &install.Installer{
			Files: o.Files, Apt: o.Apt, Programs: o.Programs, AppArmor: o.AppArmor, Phortos: o.Phortos, Layout: o.Layout, Self: o.Self, Platform: o.Platform, Clock: o.Clock, Log: o.Log,
		},
		detector: &detect.Detector{Files: o.Files, Phortos: o.Phortos, Layout: o.Layout, Clock: o.Clock, Within: o.DetectWithin},
		verifier: &verify.Verifier{Files: o.Files, Apt: o.Apt, Phortos: o.Phortos, Layout: o.Layout},
	}, nil
}

// Detect is what apt runs per host.
func (h *Hosts) Detect(ctx context.Context, url string) string { return h.detector.Detect(ctx, url) }

// Verify runs an apt update and checks it went through phortos cleanly.
func (h *Hosts) Verify(ctx context.Context) (hostplan.Report, error) { return h.verifier.Verify(ctx) }
