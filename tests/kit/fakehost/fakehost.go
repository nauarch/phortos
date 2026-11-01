// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package fakehost is a host for phortosctl's tests: files in memory, an apt and programs that
// answer what they are told, a phortos that answers what it is told and counts what it was asked,
// and a bundle signed the way phortos signs one.
package fakehost

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

// Files are files in memory, with their modes.
type Files struct {
	mu    sync.Mutex
	data  map[string][]byte
	modes map[string]uint32
}

var _ host.Files = (*Files)(nil)

// NewFiles is an empty filesystem.
func NewFiles() *Files { return &Files{data: map[string][]byte{}, modes: map[string]uint32{}} }

// Read reads a file.
func (f *Files) Read(p string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.data[p]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, errkind.ErrNotFound)
	}
	return slices.Clone(b), nil
}

// Write writes a file.
func (f *Files) Write(p string, data []byte, perm uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[p], f.modes[p] = slices.Clone(data), perm
	return nil
}

// List names a folder's entries.
func (f *Files) List(dir string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for p := range f.data {
		if path.Dir(p) == dir {
			out = append(out, path.Base(p))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", dir, errkind.ErrNotFound)
	}
	slices.Sort(out)
	return out, nil
}

// Remove removes a file or everything under a folder.
func (f *Files) Remove(p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for q := range f.data {
		if q == p || strings.HasPrefix(q, p+"/") {
			delete(f.data, q)
		}
	}
	return nil
}

// Mode is the mode a file was written with.
func (f *Files) Mode(p string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.modes[p]
}

// Apt answers apt-config with Dump and an update with Out and Err.
type Apt struct {
	Dump string
	Out  string
	Err  error
	// During runs inside the update, as apt's requests would.
	During func()
}

var _ host.Apt = (*Apt)(nil)

// Config is the dump.
func (a *Apt) Config(context.Context) (string, error) { return a.Dump, nil }

// Update gives Out and Err, running During first.
func (a *Apt) Update(context.Context) (string, error) {
	if a.During != nil {
		a.During()
	}
	return a.Out, a.Err
}

// Programs records the programs run, each as its path and arguments; Answer, when set, gives a
// run's output and error, else it prints a phortosctl's version.
type Programs struct {
	Answer func(path string, args []string) (string, error)
	Ran    []string
}

var _ host.Programs = (*Programs)(nil)

// Run records the run and answers as Answer says.
func (p *Programs) Run(_ context.Context, path string, args ...string) (string, error) {
	p.Ran = append(p.Ran, strings.Join(append([]string{path}, args...), " "))
	if p.Answer == nil {
		return "phortosctl v2 (fake)\n", nil
	}
	return p.Answer(path, args)
}

// AppArmor records the profiles reloaded, each with the local additions on Files then; Fail, when
// set, decides whether a reload fails.
type AppArmor struct {
	Files    *Files
	Local    string
	Fail     func(local []byte) error
	Reloaded []string
}

var _ host.AppArmor = (*AppArmor)(nil)

// Reload records the profile, failing as Fail says.
func (a *AppArmor) Reload(_ context.Context, profile string) error {
	a.Reloaded = append(a.Reloaded, profile)
	if a.Fail == nil {
		return nil
	}
	local, _ := a.Files.Read(a.Local)
	return a.Fail(local)
}

// Phortos answers with what it holds and counts what it was asked.
type Phortos struct {
	mu       sync.Mutex
	Bundle   trust.Bundle
	Key      *ecdsa.PublicKey
	TrustErr error
	Tooling  ops.Tools
	Binaries map[string][]byte
	Answer   ops.Route
	RouteErr error
	Delay    time.Duration
	Count    int64
	Asked    []string
}

var _ host.Phortos = (*Phortos)(nil)

// Trust gives the bundle and key, or TrustErr.
func (p *Phortos) Trust(_ context.Context, api, pin string) (trust.Bundle, *ecdsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Asked = append(p.Asked, "trust "+api+" "+pin)
	return p.Bundle, p.Key, p.TrustErr
}

// Tools gives Tooling, or TrustErr, as the pinned API would.
func (p *Phortos) Tools(_ context.Context, api, pin string) (ops.Tools, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Asked = append(p.Asked, "tools "+api+" "+pin)
	return p.Tooling, p.TrustErr
}

// Binary gives a binary from Binaries, or NotFound.
func (p *Phortos) Binary(_ context.Context, api, pin, name string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Asked = append(p.Asked, "binary "+name)
	b, ok := p.Binaries[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, errkind.ErrNotFound)
	}
	return b, nil
}

// Route gives Answer after Delay, or RouteErr; a context ending first ends it.
func (p *Phortos) Route(ctx context.Context, server, url string) (ops.Route, error) {
	p.mu.Lock()
	p.Asked = append(p.Asked, "route "+server+" "+url)
	r, err, delay := p.Answer, p.RouteErr, p.Delay
	p.mu.Unlock()
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return ops.Route{}, ctx.Err()
	}
	return r, err
}

// Requests gives Count.
func (p *Phortos) Requests(context.Context, string) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Count, nil
}

// Add adds to Count, as requests reaching phortos would.
func (p *Phortos) Add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Count += n
}

// Signed is a bundle for hosts as phortos publishes it at now, one working CA valid for a month,
// with the identity's public key and pin.
func Signed(hosts []string, now time.Time) (trust.Bundle, *ecdsa.PublicKey, string, error) {
	id, err := certs.NewKey()
	if err != nil {
		return trust.Bundle{}, nil, "", err
	}
	pin := certs.Pin(&id.PublicKey)
	ca, err := certs.NewAuthority(hosts, now, 30*24*time.Hour)
	if err != nil {
		return trust.Bundle{}, nil, "", err
	}
	sig, err := seal.Sign(id, ca.Cert.Raw)
	if err != nil {
		return trust.Bundle{}, nil, "", err
	}
	return trust.Bundle{Pin: pin, Hosts: hosts, CAs: []trust.CA{{
		PEM: string(certs.EncodeCert(ca.Cert)), Fingerprint: seal.Fingerprint(ca.Cert.Raw), NotAfter: ca.Cert.NotAfter, Signature: sig,
	}}}, &id.PublicKey, pin, nil
}
