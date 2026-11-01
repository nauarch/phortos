// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package seal_test

// Verifies:
//   - The pin is "sha256//" and the base64 SHA-256 of the key's DER form, as curl names it
//   - The fingerprint is the hex SHA-256 of the DER bytes
//   - A signature checks out for its bytes and key, and fails for other bytes, another key or junk

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/seal"
)

func TestPinAndFingerprintAreSHA256(t *testing.T) {
	if got := seal.Pin([]byte("abc")); got != "sha256//ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=" {
		t.Error(got)
	}
	if got := seal.Fingerprint([]byte("abc")); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Error(got)
	}
}

func TestSignaturesCheck(t *testing.T) {
	id, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sig, err := seal.Sign(id, []byte("ca one"))
	if err != nil {
		t.Fatal(err)
	}
	if !seal.Check(&id.PublicKey, []byte("ca one"), sig) {
		t.Error("a good signature failed")
	}
	if seal.Check(&id.PublicKey, []byte("ca two"), sig) || seal.Check(&other.PublicKey, []byte("ca one"), sig) || seal.Check(&id.PublicKey, []byte("ca one"), "!!") {
		t.Error("a wrong signature passed")
	}
}
