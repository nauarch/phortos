// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package rotation_test

// Verifies:
//   - With no CA, a current one is made
//   - A fresh CA is kept; past half its life a next one is made, once
//   - Near its end the current CA gives way to a usable next one, or to a new one
//   - A CA made for other hosts is replaced at once, and a next one for other hosts is not
//     promoted

import (
	"slices"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/features/trust/domain/rotation"
)

const life = 30 * 24 * time.Hour

var (
	now   = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	hosts = []string{"download.docker.com"}
	other = []string{"apt.example.org"}
)

// ca ends after d.
func ca(d time.Duration, h []string) *rotation.CA {
	return &rotation.CA{NotAfter: now.Add(d), Hosts: h}
}

func TestThePlanFollowsTheCAsLives(t *testing.T) {
	day := 24 * time.Hour
	for name, c := range map[string]struct {
		current, next *rotation.CA
		want          []rotation.Step
	}{
		"none":                      {nil, nil, []rotation.Step{rotation.NewCurrent}},
		"fresh":                     {ca(29*day, hosts), nil, nil},
		"past half, no next":        {ca(14*day, hosts), nil, []rotation.Step{rotation.NewNext}},
		"past half, next made":      {ca(14*day, hosts), ca(29*day, hosts), nil},
		"near its end, next ready":  {ca(12*time.Hour, hosts), ca(15*day, hosts), []rotation.Step{rotation.Promote}},
		"near its end, no next":     {ca(12*time.Hour, hosts), nil, []rotation.Step{rotation.NewCurrent}},
		"promoted next past half":   {ca(12*time.Hour, hosts), ca(10*day, hosts), []rotation.Step{rotation.Promote, rotation.NewNext}},
		"other hosts":               {ca(29*day, other), ca(29*day, other), []rotation.Step{rotation.NewCurrent}},
		"next for other hosts":      {ca(14*day, hosts), ca(29*day, other), []rotation.Step{rotation.NewNext}},
		"expired, next other hosts": {ca(-day, hosts), ca(29*day, other), []rotation.Step{rotation.NewCurrent}},
	} {
		if got := rotation.Plan(c.current, c.next, hosts, now, life); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
