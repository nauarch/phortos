// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package state is what phortosctl keeps on a host between runs: where phortos is, the pin it
// trusts, the hosts phortos intercepts and the working CAs installed for them, and the hosts with
// credentials. configure writes it, refresh keeps it current, detect reads it.
package state

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// State is the kept state. Server is phortos' proxy address (host:port), API its TLS API
// (host:port), Pin the identity's pin; all three are set by configure only.
//
// Hosts are the intercepted hosts whose CaInfo every apt run has loaded; Pending are intercepted
// hosts whose CaInfo was written by the refresh of a run already under way, which loaded its
// configuration before, so for that run they go straight. The next refresh makes them Hosts.
type State struct {
	Server       string    `json:"server"`
	API          string    `json:"api,omitempty"`
	Pin          string    `json:"pin,omitempty"`
	Hosts        []string  `json:"hosts,omitempty"`
	Pending      []string  `json:"pending,omitempty"`
	CAs          []CA      `json:"cas,omitempty"`
	Credentialed []string  `json:"credentialed,omitempty"`
	Refreshed    time.Time `json:"refreshed,omitzero"`
	// NoAutoUpgrade keeps refresh from installing the phortosctl phortos hands out; set by
	// configure only.
	NoAutoUpgrade bool `json:"no_auto_upgrade,omitempty"`
}

// Written are the hosts the apt configuration names: Hosts and Pending, sorted.
func (s State) Written() []string {
	out := append(slices.Clone(s.Hosts), s.Pending...)
	slices.Sort(out)
	return slices.Compact(out)
}

// Settle moves to intercepted hosts now: those the apt configuration already named are Hosts,
// the others Pending, for the next refresh to settle.
func (s *State) Settle(now []string) {
	written := s.Written()
	s.Hosts, s.Pending = nil, nil
	for _, h := range now {
		if slices.Contains(written, h) {
			s.Hosts = append(s.Hosts, h)
		} else {
			s.Pending = append(s.Pending, h)
		}
	}
}

// CA is an installed working CA: its fingerprint and when it is valid.
type CA struct {
	Fingerprint string    `json:"fingerprint"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
}

// Intercepts reports whether host's certificates are checked against the installed bundle.
func (s State) Intercepts(host string) bool { return slices.Contains(s.Hosts, host) }

// HasCredentials reports whether host is named in apt's credentials.
func (s State) HasCredentials(host string) bool { return slices.Contains(s.Credentialed, host) }

// Valid reports whether the CA with fingerprint is installed and valid at now.
func (s State) Valid(fingerprint string, now time.Time) bool {
	for _, c := range s.CAs {
		if c.Fingerprint == fingerprint && !now.Before(c.NotBefore) && now.Before(c.NotAfter) {
			return true
		}
	}
	return false
}

// Encode is the state as the file holds it.
func Encode(s State) []byte {
	b, _ := json.MarshalIndent(s, "", "  ")
	return append(b, '\n')
}

// Decode reads a state file; one without a server is invalid.
func Decode(b []byte) (State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("phortosctl state: %w: %w", errkind.ErrInvalid, err)
	}
	if s.Server == "" {
		return State{}, fmt.Errorf("phortosctl state names no server: %w", errkind.ErrInvalid)
	}
	return s, nil
}
