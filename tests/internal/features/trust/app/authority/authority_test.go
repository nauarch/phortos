// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package authority_test

// Verifies:
//   - An identity key that is there but unreadable stops the start instead of being replaced,
//     which would break every client's pin
//   - A damaged working CA file is replaced by a new CA, keeping the identity

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/storage/keys"
	"github.com/nauarch/phortos/internal/features/trust/app/authority"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
)

// newAuthority is an authority over a key folder for download.docker.com.
func newAuthority(t *testing.T, dir string) *authority.Authority {
	t.Helper()
	ks, err := keys.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &authority.Authority{
		Keys: ks, Hosts: []string{"download.docker.com"}, CALifetime: 30 * 24 * time.Hour, LeafLifetime: 24 * time.Hour,
		Clock: kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)), Log: slog.New(slog.DiscardHandler),
	}
}

func TestAnUnreadableIdentityStopsTheStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	if err := newAuthority(t, dir).Start(); err != nil {
		t.Fatal(err)
	}
	ks, _ := keys.Open(dir)
	_ = ks.Save("identity.pem", []byte("damaged"))
	if err := newAuthority(t, dir).Start(); err == nil {
		t.Fatal("a damaged identity was replaced")
	}
	if b, _ := ks.Load("identity.pem"); string(b) != "damaged" {
		t.Error("the damaged identity was overwritten")
	}
}

func TestADamagedCAIsMadeAgain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	a := newAuthority(t, dir)
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	pin := a.Pin()
	ks, _ := keys.Open(dir)
	_ = ks.Save("ca-current.pem", []byte("damaged"))
	again := newAuthority(t, dir)
	if err := again.Start(); err != nil {
		t.Fatal(err)
	}
	if again.Pin() != pin || len(again.Bundle().CAs) != 1 {
		t.Errorf("pin kept: %v, CAs %d", again.Pin() == pin, len(again.Bundle().CAs))
	}
}
