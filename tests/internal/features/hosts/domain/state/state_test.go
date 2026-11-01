// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package state_test

// Verifies:
//   - A state survives its file; a file that is not JSON or names no server is invalid
//   - A CA is valid only once installed and only within its dates
//   - Interception and credentials are looked up by exact host
//   - A newly intercepted host is pending until the next settle; a host gone is dropped at once

import (
	"slices"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func TestAStateSurvivesItsFile(t *testing.T) {
	s := state.State{
		Server: "phortos.lan:3142", API: "phortos.lan:3143", Pin: "sha256//x", Hosts: []string{"download.docker.com"},
		CAs:          []state.CA{{Fingerprint: "aa", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}},
		Credentialed: []string{"enterprise.proxmox.com"}, Refreshed: now,
	}
	back, err := state.Decode(state.Encode(s))
	if err != nil || back.Server != s.Server || back.Pin != s.Pin || !back.CAs[0].NotAfter.Equal(s.CAs[0].NotAfter) || !back.Refreshed.Equal(now) {
		t.Fatalf("%+v %v", back, err)
	}
	for _, bad := range []string{"junk", "{}", `{"server":""}`} {
		if _, err := state.Decode([]byte(bad)); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestACAIsValidOnlyWithinItsDates(t *testing.T) {
	s := state.State{CAs: []state.CA{{Fingerprint: "aa", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}}}
	if !s.Valid("aa", now) || s.Valid("bb", now) || s.Valid("aa", now.Add(-2*time.Hour)) || s.Valid("aa", now.Add(time.Hour)) {
		t.Error("validity is wrong")
	}
}

func TestNewHostsSettleOneRefreshLater(t *testing.T) {
	s := state.State{Hosts: []string{"a.example"}, Pending: []string{"b.example"}}
	if w := s.Written(); len(w) != 2 {
		t.Errorf("written %v", w)
	}
	s.Settle([]string{"b.example", "c.example"})
	if !slices.Equal(s.Hosts, []string{"b.example"}) || !slices.Equal(s.Pending, []string{"c.example"}) {
		t.Errorf("%+v", s)
	}
	s.Settle([]string{"b.example", "c.example"})
	if !slices.Equal(s.Hosts, []string{"b.example", "c.example"}) || len(s.Pending) != 0 {
		t.Errorf("%+v", s)
	}
}

func TestHostsAreLookedUpExactly(t *testing.T) {
	s := state.State{Hosts: []string{"download.docker.com"}, Credentialed: []string{"enterprise.proxmox.com"}}
	if !s.Intercepts("download.docker.com") || s.Intercepts("docker.com") || !s.HasCredentials("enterprise.proxmox.com") || s.HasCredentials("download.proxmox.com") {
		t.Error("lookups are wrong")
	}
}
