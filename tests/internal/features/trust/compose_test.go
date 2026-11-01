// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package trust_test

// Verifies:
//   - The first start makes an identity and a working CA; a restart keeps both, so nothing
//     clients installed changes
//   - The bundle carries the pin, the hosts and the CAs, each signed by the identity and named by
//     the SHA-256 of its DER bytes
//   - A certificate is issued only for an intercepted host, verifies against the bundle's CA for
//     that name, and is reused until a quarter of its life is left
//   - Past half its life a next CA joins the bundle; near its end the next one takes over and the
//     certificates come from it
//   - Changing the intercepted hosts makes a new CA at once, keeping the identity
//   - Hosts with wildcards or ports are invalid
//   - No hosts give the identity (same pin) and no CAs; CAs kept from before are dropped

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/storage/keys"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"

	feature "github.com/nauarch/phortos/internal/features/trust"
)

const docker = "download.docker.com"

// start composes the authority over a key folder.
func start(t *testing.T, dir string, clk *kitclock.Fixed, hosts ...string) *feature.Trust {
	t.Helper()
	ks, err := keys.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := feature.Compose(feature.Options{Keys: ks, Hosts: hosts, Clock: clk, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// caOf parses a bundle's CA.
func caOf(t *testing.T, c trust.CA) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(c.PEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// identityOf is the public key the API presents.
func identityOf(t *testing.T, tr *feature.Trust) *ecdsa.PublicKey {
	t.Helper()
	cert, err := x509.ParseCertificate(tr.Identity().Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return cert.PublicKey.(*ecdsa.PublicKey)
}

func TestARestartKeepsTheIdentityAndTheCA(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	clk := kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	first := start(t, dir, clk, "Download.Docker.com", "developer.download.nvidia.com")
	clk.Advance(time.Hour)
	again := start(t, dir, clk, "developer.download.nvidia.com", docker)
	if first.Pin() != again.Pin() {
		t.Error("the pin changed across a restart")
	}
	a, b := first.Bundle(), again.Bundle()
	if len(a.CAs) != 1 || len(b.CAs) != 1 || a.CAs[0].PEM != b.CAs[0].PEM {
		t.Errorf("the CA changed across a restart: %d %d", len(a.CAs), len(b.CAs))
	}
}

func TestTheBundleIsSignedAndCertificatesVerify(t *testing.T) {
	clk := kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	tr := start(t, t.TempDir(), clk, docker)
	b := tr.Bundle()
	if b.Pin != certs.Pin(identityOf(t, tr)) || len(b.Hosts) != 1 || b.Hosts[0] != docker {
		t.Fatalf("%+v", b)
	}
	ca := caOf(t, b.CAs[0])
	if !seal.Check(identityOf(t, tr), ca.Raw, b.CAs[0].Signature) {
		t.Error("the CA's signature does not check")
	}
	if sum := sha256.Sum256(ca.Raw); b.CAs[0].Fingerprint != hex.EncodeToString(sum[:]) {
		t.Errorf("fingerprint %s", b.CAs[0].Fingerprint)
	}
	leaf, err := tr.Certificate(docker)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: docker, CurrentTime: clk.Now()}); err != nil {
		t.Error(err)
	}
	if again, _ := tr.Certificate(docker); again != leaf {
		t.Error("a fresh certificate was not reused")
	}
	clk.Advance(20 * time.Hour)
	if later, _ := tr.Certificate(docker); later == leaf {
		t.Error("a certificate near its end was reused")
	}
	if _, err := tr.Certificate("deb.debian.org"); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("a host not intercepted: %v", err)
	}
	if !tr.Intercepts("DOWNLOAD.docker.com") || tr.Intercepts("deb.debian.org") {
		t.Error("Intercepts is wrong")
	}
}

func TestTheCAsRotate(t *testing.T) {
	clk := kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	tr := start(t, t.TempDir(), clk, docker)
	first := caOf(t, tr.Bundle().CAs[0])
	clk.Advance(16 * 24 * time.Hour)
	if err := tr.Rotate(); err != nil {
		t.Fatal(err)
	}
	b := tr.Bundle()
	if len(b.CAs) != 2 || caOf(t, b.CAs[0]).SerialNumber.Cmp(first.SerialNumber) != 0 {
		t.Fatalf("past half its life: %d CAs", len(b.CAs))
	}
	next := caOf(t, b.CAs[1])
	clk.Advance(14 * 24 * time.Hour)
	if err := tr.Rotate(); err != nil {
		t.Fatal(err)
	}
	b = tr.Bundle()
	if caOf(t, b.CAs[0]).SerialNumber.Cmp(next.SerialNumber) != 0 {
		t.Fatal("the next CA did not take over")
	}
	leaf, _ := tr.Certificate(docker)
	if err := leaf.Leaf.CheckSignatureFrom(next); err != nil {
		t.Errorf("certificates do not come from the new CA: %v", err)
	}
}

func TestOtherHostsMakeANewCA(t *testing.T) {
	dir := t.TempDir()
	clk := kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	before := start(t, dir, clk, docker)
	after := start(t, dir, clk, docker, "developer.download.nvidia.com")
	if before.Pin() != after.Pin() {
		t.Error("the identity changed with the hosts")
	}
	if before.Bundle().CAs[0].PEM == after.Bundle().CAs[0].PEM {
		t.Error("the CA did not change with the hosts")
	}
	if _, err := after.Certificate("developer.download.nvidia.com"); err != nil {
		t.Error(err)
	}
}

func TestBadHostsAreInvalid(t *testing.T) {
	ks, _ := keys.Open(t.TempDir())
	for _, h := range [][]string{{"*.docker.com"}, {"download.docker.com:443"}} {
		if _, err := feature.Compose(feature.Options{Keys: ks, Hosts: h}); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%v: %v", h, err)
		}
	}
}

func TestNoHostsGiveAnIdentityAndNoCAs(t *testing.T) {
	dir := t.TempDir()
	clk := kitclock.At(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	before := start(t, dir, clk, docker)
	none := start(t, dir, clk)
	if none.Pin() != before.Pin() {
		t.Error("the identity changed")
	}
	if b := none.Bundle(); len(b.Hosts) != 0 || len(b.CAs) != 0 || b.Pin != before.Pin() {
		t.Errorf("%+v", b)
	}
	if none.Intercepts(docker) {
		t.Error("a host is intercepted")
	}
	if _, err := none.Certificate(docker); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("%v", err)
	}
	if again := start(t, dir, clk, docker); again.Bundle().CAs[0].PEM == before.Bundle().CAs[0].PEM {
		t.Error("a dropped CA came back")
	}
}
