// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package bundle checks what phortos publishes before a host installs it, and builds the file apt
// checks intercepted hosts against. The identity's key comes from the pinned connection; every
// working CA must carry its signature, so a CA nobody holding the identity made is refused.
package bundle

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"slices"
	"time"

	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

// Checked is a bundle that passed: its hosts, its CAs as the state keeps them, and their PEM.
type Checked struct {
	Hosts []string
	CAs   []state.CA
	PEM   [][]byte
}

// Check checks a published bundle against the pin the host trusts and the identity key the
// pinned connection presented. The key must match the pin and the bundle must name the same pin;
// each CA must parse, be a CA, match its fingerprint and carry the identity's signature; CAs
// already expired at now are dropped; at least one must be left.
func Check(b trust.Bundle, identity *ecdsa.PublicKey, pin string, now time.Time) (Checked, error) {
	spki, err := x509.MarshalPKIXPublicKey(identity)
	if err != nil || seal.Pin(spki) != pin {
		return Checked{}, fmt.Errorf("phortos' key is not the pinned one: %w", errkind.ErrForbidden)
	}
	if b.Pin != pin {
		return Checked{}, fmt.Errorf("the bundle names pin %s, not %s: %w", b.Pin, pin, errkind.ErrForbidden)
	}
	out := Checked{Hosts: slices.Sorted(slices.Values(b.Hosts))}
	for i, ca := range b.CAs {
		block, _ := pem.Decode([]byte(ca.PEM))
		if block == nil || block.Type != "CERTIFICATE" {
			return Checked{}, fmt.Errorf("working CA %d is not a PEM certificate: %w", i, errkind.ErrInvalid)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return Checked{}, fmt.Errorf("working CA %d: %w: %w", i, errkind.ErrInvalid, err)
		}
		switch {
		case !cert.IsCA:
			return Checked{}, fmt.Errorf("working CA %d is not a CA: %w", i, errkind.ErrInvalid)
		case seal.Fingerprint(cert.Raw) != ca.Fingerprint:
			return Checked{}, fmt.Errorf("working CA %d does not match its fingerprint: %w", i, errkind.ErrInvalid)
		case !seal.Check(identity, cert.Raw, ca.Signature):
			return Checked{}, fmt.Errorf("working CA %s: the identity's signature does not verify: %w", ca.Fingerprint, errkind.ErrForbidden)
		case !now.Before(cert.NotAfter):
			continue
		}
		out.CAs = append(out.CAs, state.CA{Fingerprint: ca.Fingerprint, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter})
		out.PEM = append(out.PEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	}
	if len(out.CAs) == 0 && len(b.Hosts) > 0 {
		return Checked{}, fmt.Errorf("the bundle holds no working CA valid now: %w", errkind.ErrInvalid)
	}
	return out, nil
}

// File is the CA file apt checks intercepted hosts against: the host's own CAs, so a host that
// goes straight still verifies the real repository, then phortos' working CAs.
func File(system []byte, c Checked) []byte {
	var b bytes.Buffer
	b.Write(system)
	if len(system) > 0 && system[len(system)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("# phortos working CAs, added by phortosctl\n")
	for _, p := range c.PEM {
		b.Write(p)
	}
	return b.Bytes()
}
