// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package certs_test

// Verifies:
//   - The pin is "sha256//" and the base64 SHA-256 of the public key's DER form
//   - A working CA vouches for its hosts (and their subdomains) and nothing else: a leaf it
//     issues for another host fails verification
//   - A leaf never outlives its CA, and starts before now so slow clocks accept it
//   - Keys and CAs survive PEM; a CA file whose key does not match its certificate is invalid
//   - The identity certificate carries the identity key

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// key is a fresh key.
func key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := certs.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestThePinIsTheKeysSHA256(t *testing.T) {
	k := key(t)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	sum := sha256.Sum256(der)
	if got := certs.Pin(&k.PublicKey); got != "sha256//"+base64.StdEncoding.EncodeToString(sum[:]) {
		t.Error(got)
	}
}

func TestAWorkingCAVouchesOnlyForItsHosts(t *testing.T) {
	a, err := certs.NewAuthority([]string{"download.docker.com", "Developer.Download.NVIDIA.com"}, now, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if h := a.Hosts(); len(h) != 2 || h[0] != "developer.download.nvidia.com" {
		t.Errorf("hosts %v", h)
	}
	roots := x509.NewCertPool()
	roots.AddCert(a.Cert)
	for host, ok := range map[string]bool{"download.docker.com": true, "developer.download.nvidia.com": true, "deb.debian.org": false} {
		leaf, err := a.Leaf(host, now, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		_, err = leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, CurrentTime: now})
		if (err == nil) != ok {
			t.Errorf("%s: %v", host, err)
		}
	}
	if _, err := certs.NewAuthority(nil, now, time.Hour); errkind.Of(err) != errkind.Invalid {
		t.Errorf("no hosts: %v", err)
	}
}

func TestALeafNeverOutlivesItsCA(t *testing.T) {
	a, _ := certs.NewAuthority([]string{"download.docker.com"}, now, 12*time.Hour)
	leaf, err := a.Leaf("download.docker.com", now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Leaf.NotAfter.After(a.Cert.NotAfter) || !leaf.Leaf.NotBefore.Before(now) {
		t.Errorf("leaf %v..%v, CA until %v", leaf.Leaf.NotBefore, leaf.Leaf.NotAfter, a.Cert.NotAfter)
	}
}

func TestKeysAndCAsSurvivePEM(t *testing.T) {
	k := key(t)
	pem, err := certs.EncodeKey(k)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := certs.DecodeKey(pem); err != nil || !back.Equal(k) {
		t.Errorf("key: %v", err)
	}
	if _, err := certs.DecodeKey([]byte("junk")); errkind.Of(err) != errkind.Invalid {
		t.Errorf("junk key: %v", err)
	}
	a, _ := certs.NewAuthority([]string{"download.docker.com"}, now, time.Hour)
	file, _ := certs.EncodeAuthority(a)
	back, err := certs.DecodeAuthority(file)
	if err != nil || !back.Cert.Equal(a.Cert) {
		t.Fatalf("CA: %v", err)
	}
	other, _ := certs.EncodeKey(key(t))
	if _, err := certs.DecodeAuthority(append(certs.EncodeCert(a.Cert), other...)); errkind.Of(err) != errkind.Invalid {
		t.Errorf("mismatched CA file: %v", err)
	}
}

func TestTheIdentityCertificateCarriesTheIdentityKey(t *testing.T) {
	k := key(t)
	c, err := certs.IdentityCertificate(k, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := x509.ParseCertificate(c.Certificate[0])
	if certs.Pin(parsed.PublicKey.(*ecdsa.PublicKey)) != certs.Pin(&k.PublicKey) {
		t.Error("the certificate's key is not the identity")
	}
}
