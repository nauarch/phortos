// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package authority runs phortos' certificates: it loads (or makes, once) the identity key,
// keeps the current and next working CAs on disk so restarts change nothing clients installed,
// rotates them as the rotation plan says, signs each with the identity, and issues and reuses
// the certificates presented for intercepted hosts.
package authority

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nauarch/phortos/internal/contracts/keys"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/features/trust/domain/rotation"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

// The files the key store holds.
const (
	identityFile = "identity.pem"
	currentFile  = "ca-current.pem"
	nextFile     = "ca-next.pem"
)

// identityLifetime is how long the API's own certificate is made for; clients check its key, not
// its dates, and it is made afresh at every start.
const identityLifetime = 365 * 24 * time.Hour

// Authority is safe for concurrent use. Hosts are the intercepted hosts, sorted and lower case;
// once started, they change only through SetHosts.
type Authority struct {
	Keys         keys.Store
	Hosts        []string
	CALifetime   time.Duration
	LeafLifetime time.Duration
	Clock        clock.Clock
	Log          *slog.Logger

	mu       sync.Mutex
	identity *ecdsa.PrivateKey
	idCert   tls.Certificate
	current  *certs.Authority
	next     *certs.Authority
	sigs     map[*x509.Certificate]string
	leaves   map[string]*tls.Certificate
}

// Start loads or makes the identity and the working CAs, and rotates them when due.
func (a *Authority) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := a.loadIdentity()
	if err != nil {
		return err
	}
	a.identity = id
	if a.idCert, err = certs.IdentityCertificate(id, a.Clock.Now(), identityLifetime); err != nil {
		return err
	}
	a.current = a.loadCA(currentFile)
	a.next = a.loadCA(nextFile)
	a.sigs = map[*x509.Certificate]string{}
	a.leaves = map[string]*tls.Certificate{}
	return a.rotate()
}

// loadIdentity reads the identity key, making and saving one when there is none. A key that is
// there but unreadable stops phortos: making another would break every client's pin.
func (a *Authority) loadIdentity() (*ecdsa.PrivateKey, error) {
	b, err := a.Keys.Load(identityFile)
	switch {
	case err == nil:
		return certs.DecodeKey(b)
	case errkind.Of(err) == errkind.NotFound:
		k, err := certs.NewKey()
		if err != nil {
			return nil, err
		}
		pem, err := certs.EncodeKey(k)
		if err != nil {
			return nil, err
		}
		if err := a.Keys.Save(identityFile, pem); err != nil {
			return nil, err
		}
		a.Log.Info("identity made", "pin", certs.Pin(&k.PublicKey))
		return k, nil
	}
	return nil, fmt.Errorf("identity: %w", err)
}

// loadCA reads a kept working CA; one that is missing or unreadable is made again by rotation.
func (a *Authority) loadCA(name string) *certs.Authority {
	b, err := a.Keys.Load(name)
	if err != nil {
		if errkind.Of(err) != errkind.NotFound {
			a.Log.Warn("working CA not loaded", "file", name, "err", err)
		}
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	ca, err := certs.DecodeAuthority(b)
	if err != nil {
		a.Log.Warn("working CA not loaded", "file", name, "err", err)
		return nil
	}
	return &ca
}

// Rotate changes the working CAs when the plan says so.
func (a *Authority) Rotate() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rotate()
}

// rotate applies the plan; the caller holds the lock. With no hosts to intercept there are no
// working CAs at all, and any kept from before are dropped.
func (a *Authority) rotate() error {
	if len(a.Hosts) == 0 {
		if a.current != nil || a.next != nil {
			a.Log.Info("working CAs dropped: no hosts are intercepted")
		}
		a.current, a.next = nil, nil
		a.leaves = map[string]*tls.Certificate{}
		return errors.Join(a.save(currentFile, nil), a.save(nextFile, nil))
	}
	now := a.Clock.Now()
	for _, step := range rotation.Plan(spanOf(a.current), spanOf(a.next), a.Hosts, now, a.CALifetime) {
		switch step {
		case rotation.Promote:
			a.current, a.next = a.next, nil
			a.leaves = map[string]*tls.Certificate{}
			a.Log.Info("working CA promoted", "until", a.current.Cert.NotAfter)
		case rotation.NewCurrent:
			ca, err := certs.NewAuthority(a.Hosts, now, a.CALifetime)
			if err != nil {
				return err
			}
			a.current, a.next = &ca, nil
			a.leaves = map[string]*tls.Certificate{}
			a.Log.Info("working CA made", "hosts", a.Hosts, "until", ca.Cert.NotAfter)
		case rotation.NewNext:
			ca, err := certs.NewAuthority(a.Hosts, now, a.CALifetime)
			if err != nil {
				return err
			}
			a.next = &ca
			a.Log.Info("next working CA made", "until", ca.Cert.NotAfter)
		}
	}
	if err := a.save(currentFile, a.current); err != nil {
		return err
	}
	return a.save(nextFile, a.next)
}

// save keeps a working CA, or empties its file when there is none.
func (a *Authority) save(name string, ca *certs.Authority) error {
	if ca == nil {
		return a.Keys.Save(name, nil)
	}
	b, err := certs.EncodeAuthority(*ca)
	if err != nil {
		return err
	}
	return a.Keys.Save(name, b)
}

// spanOf is what rotation needs of a CA.
func spanOf(ca *certs.Authority) *rotation.CA {
	if ca == nil {
		return nil
	}
	return &rotation.CA{NotAfter: ca.Cert.NotAfter, Hosts: ca.Hosts()}
}

// SetHosts changes the intercepted hosts (sorted and lower case) while phortos runs: a working
// CA for the new set is made at once, and clients install it at their next refresh.
func (a *Authority) SetHosts(hosts []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.Equal(a.Hosts, hosts) {
		return nil
	}
	a.Hosts = slices.Clone(hosts)
	return a.rotate()
}

// Intercepts reports whether phortos ends TLS for host.
func (a *Authority) Intercepts(host string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.Hosts, strings.ToLower(host))
}

// Certificate is the certificate for an intercepted host, reused until a quarter of its life is
// left or the current CA changes.
func (a *Authority) Certificate(host string) (*tls.Certificate, error) {
	host = strings.ToLower(host)
	if !a.Intercepts(host) {
		return nil, fmt.Errorf("%s is not intercepted: %w", host, errkind.ErrForbidden)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.Clock.Now()
	if c, ok := a.leaves[host]; ok && c.Leaf.NotAfter.Sub(now) > a.LeafLifetime/4 {
		return c, nil
	}
	c, err := a.current.Leaf(host, now, a.LeafLifetime)
	if err != nil {
		return nil, err
	}
	a.leaves[host] = c
	return c, nil
}

// Bundle is what clients install: the pin, the hosts, and the current and next CAs, signed.
func (a *Authority) Bundle() trust.Bundle {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := trust.Bundle{Pin: certs.Pin(&a.identity.PublicKey), Hosts: a.Hosts}
	for _, ca := range []*certs.Authority{a.current, a.next} {
		if ca == nil {
			continue
		}
		sig, ok := a.sigs[ca.Cert]
		if !ok {
			var err error
			if sig, err = seal.Sign(a.identity, ca.Cert.Raw); err != nil {
				a.Log.Error("working CA not signed", "err", err)
				continue
			}
			a.sigs[ca.Cert] = sig
		}
		b.CAs = append(b.CAs, trust.CA{
			PEM: string(certs.EncodeCert(ca.Cert)), Fingerprint: seal.Fingerprint(ca.Cert.Raw), NotAfter: ca.Cert.NotAfter, Signature: sig,
		})
	}
	return b
}

// Identity is the API's certificate.
func (a *Authority) Identity() tls.Certificate {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.idCert
}

// Pin is the identity's pin.
func (a *Authority) Pin() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return certs.Pin(&a.identity.PublicKey)
}
