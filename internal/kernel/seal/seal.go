// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package seal is how phortos and phortosctl name and vouch for keys and certificates: the pin
// of a public key, the fingerprint of a certificate, and the identity's signature over a working
// CA. Both sides use these same functions, so what one makes the other checks.
package seal

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Pin names a public key as curl's --pinnedpubkey does: "sha256//" and the base64 SHA-256 of its
// DER form (SubjectPublicKeyInfo).
func Pin(spki []byte) string {
	sum := sha256.Sum256(spki)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
}

// Fingerprint names a certificate: the lower-case hex SHA-256 of its DER form, as
// `openssl x509 -fingerprint -sha256` gives it without the colons.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Sign is the identity's signature over a working CA's DER bytes: ECDSA, ASN.1, over SHA-256,
// base64.
func Sign(identity *ecdsa.PrivateKey, der []byte) (string, error) {
	sum := sha256.Sum256(der)
	sig, err := ecdsa.SignASN1(rand.Reader, identity, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing a working CA: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Check reports whether sig is the identity's signature over a working CA's DER bytes.
func Check(identity *ecdsa.PublicKey, der []byte, sig string) bool {
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(der)
	return ecdsa.VerifyASN1(identity, sum[:], raw)
}
