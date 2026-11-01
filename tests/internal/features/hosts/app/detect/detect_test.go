// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package detect_test

// Verifies:
//   - Without a state file, or with a damaged one, the answer is DIRECT and phortos is not asked
//   - A phortos slower than the deadline costs at most the deadline, then DIRECT
//   - A phortos that answers sends a served host through the configured server

import (
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/features/hosts/app/detect"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
	"github.com/nauarch/phortos/tests/kit/fakehost"
)

const url = "http://deb.debian.org/debian/dists/trixie/InRelease"

// detector is a detector over fakes, with a 100 ms deadline.
func detector(fs *fakehost.Files, ph *fakehost.Phortos) *detect.Detector {
	return &detect.Detector{Files: fs, Phortos: ph, Layout: layout.Linux(), Clock: kitclock.At(time.Now()), Within: 100 * time.Millisecond}
}

func TestNoStateIsDirect(t *testing.T) {
	fs, ph := fakehost.NewFiles(), &fakehost.Phortos{Answer: ops.Route{Proxy: "http://x:1"}}
	if got := detector(fs, ph).Detect(t.Context(), url); got != ops.Direct {
		t.Error(got)
	}
	_ = fs.Write(layout.Linux().State, []byte("{"), 0o644)
	if got := detector(fs, ph).Detect(t.Context(), url); got != ops.Direct || len(ph.Asked) != 0 {
		t.Errorf("%s, asked %v", got, ph.Asked)
	}
}

func TestASlowPhortosCostsTheDeadlineOnly(t *testing.T) {
	fs := fakehost.NewFiles()
	_ = fs.Write(layout.Linux().State, state.Encode(state.State{Server: "phortos.lan:3142"}), 0o644)
	ph := &fakehost.Phortos{Answer: ops.Route{Proxy: "http://phortos.lan:3142"}, Delay: 5 * time.Second}
	start := time.Now()
	if got := detector(fs, ph).Detect(t.Context(), url); got != ops.Direct || time.Since(start) > 2*time.Second {
		t.Errorf("%s after %s", got, time.Since(start))
	}
	ph.Delay = 0
	if got := detector(fs, ph).Detect(t.Context(), url); got != "http://phortos.lan:3142" {
		t.Error(got)
	}
}
