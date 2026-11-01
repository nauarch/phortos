// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Algorithm is the only digest algorithm phortos keeps files by: the one apt repositories list
// for their packages. An index a release lists by SHA512 only is checked against that, then kept
// by its SHA256 too.
const Algorithm = "sha256"

// Digest is a content address, sha256:<64 lower-case hex digits>.
type Digest string

// Parse reads a digest; anything but sha256 with 64 lower-case hex digits is invalid.
func Parse(s string) (Digest, error) {
	hexPart, ok := strings.CutPrefix(s, Algorithm+":")
	if !ok || len(hexPart) != sha256.Size*2 || strings.ToLower(hexPart) != hexPart {
		return "", fmt.Errorf("digest %q: want %s:<64 lower-case hex>: %w", s, Algorithm, errkind.ErrInvalid)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("digest %q: %w", s, errkind.ErrInvalid)
	}
	return Digest(s), nil
}

// Of is the digest of b.
func Of(b []byte) Digest {
	sum := sha256.Sum256(b)
	return Digest(Algorithm + ":" + hex.EncodeToString(sum[:]))
}

// Hex is the digest's hex digits, without the algorithm.
func (d Digest) Hex() string { return strings.TrimPrefix(string(d), Algorithm+":") }

// String is the digest as registries spell it.
func (d Digest) String() string { return string(d) }
