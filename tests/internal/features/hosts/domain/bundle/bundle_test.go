// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package bundle_test

// Verifies, with real certificates from phortos' own authority code:
//   - A bundle signed by the pinned identity passes, keeping its hosts and its CAs' dates
//   - A key that is not the pinned one, a bundle naming another pin, a CA signed by another key,
//     a fingerprint that does not match, a certificate that is no CA or no PEM all fail
//   - Expired CAs are dropped; a bundle with hosts but no CA valid now fails
//   - The CA file holds the host's CAs, then the working CAs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/hosts/domain/bundle"
	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// published is a bundle as phortos publishes it, with its identity and pin.
func published(t *testing.T) (trust.Bundle, *ecdsa.PrivateKey, string) {
	t.Helper()
	id, _ := certs.NewKey()
	pin := certs.Pin(&id.PublicKey)
	b := trust.Bundle{Pin: pin, Hosts: []string{"download.docker.com"}}
	for _, start := range []time.Time{now.Add(-40 * 24 * time.Hour), now} {
		ca, err := certs.NewAuthority(b.Hosts, start, 30*24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		b.CAs = append(b.CAs, sign(t, id, ca.Cert))
	}
	return b, id, pin
}

// sign is a CA as the bundle carries it, signed by id.
func sign(t *testing.T, id *ecdsa.PrivateKey, cert *x509.Certificate) trust.CA {
	t.Helper()
	sig, err := seal.Sign(id, cert.Raw)
	if err != nil {
		t.Fatal(err)
	}
	return trust.CA{PEM: string(certs.EncodeCert(cert)), Fingerprint: seal.Fingerprint(cert.Raw), NotAfter: cert.NotAfter, Signature: sig}
}

func TestASignedBundlePasses(t *testing.T) {
	b, id, pin := published(t)
	c, err := bundle.Check(b, &id.PublicKey, pin, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hosts) != 1 || len(c.CAs) != 1 || c.CAs[0].Fingerprint != b.CAs[1].Fingerprint || !c.CAs[0].NotBefore.Before(now) {
		t.Errorf("%+v", c)
	}
	file := bundle.File([]byte("SYSTEM CAS"), c)
	if !bytes.HasPrefix(file, []byte("SYSTEM CAS\n")) || !bytes.Contains(file, []byte("-----BEGIN CERTIFICATE-----")) {
		t.Errorf("%s", file)
	}
}

func TestAnythingNotVouchedForFails(t *testing.T) {
	other, _ := certs.NewKey()
	for name, c := range map[string]struct {
		mutate func(b *trust.Bundle, id **ecdsa.PrivateKey)
		kind   errkind.Kind
	}{
		"another key": {func(_ *trust.Bundle, id **ecdsa.PrivateKey) { *id = other }, errkind.Forbidden},
		"another pin": {func(b *trust.Bundle, _ **ecdsa.PrivateKey) { b.Pin = certs.Pin(&other.PublicKey) }, errkind.Forbidden},
		"another signer": {func(b *trust.Bundle, _ **ecdsa.PrivateKey) {
			ca, _ := certs.NewAuthority(b.Hosts, now, time.Hour)
			b.CAs[1] = sign(t, other, ca.Cert)
		}, errkind.Forbidden},
		"wrong fingerprint": {func(b *trust.Bundle, _ **ecdsa.PrivateKey) { b.CAs[1].Fingerprint = strings.Repeat("0", 64) }, errkind.Invalid},
		"not PEM":           {func(b *trust.Bundle, _ **ecdsa.PrivateKey) { b.CAs[1].PEM = "junk" }, errkind.Invalid},
		"no CA valid now":   {func(b *trust.Bundle, _ **ecdsa.PrivateKey) { b.CAs = b.CAs[:1] }, errkind.Invalid},
	} {
		b, id, pin := published(t)
		presented := id
		c.mutate(&b, &presented)
		if _, err := bundle.Check(b, &presented.PublicKey, pin, now); errkind.Of(err) != c.kind {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestACertificateThatIsNoCAFails(t *testing.T) {
	b, id, pin := published(t)
	ca, _ := certs.NewAuthority(b.Hosts, now, time.Hour)
	leaf, _ := ca.Leaf("download.docker.com", now, time.Hour)
	b.CAs[1] = sign(t, id, leaf.Leaf)
	if _, err := bundle.Check(b, &id.PublicKey, pin, now); errkind.Of(err) != errkind.Invalid {
		t.Errorf("%v", err)
	}
}
