// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Verifier hashes bytes as they stream past and checks them against the digest they should have.
type Verifier struct {
	want Digest
	h    hash.Hash
	n    int64
}

// NewVerifier checks bytes against want.
func NewVerifier(want Digest) *Verifier { return &Verifier{want: want, h: sha256.New()} }

// Write hashes p; it never fails.
func (v *Verifier) Write(p []byte) (int, error) {
	v.n += int64(len(p))
	return v.h.Write(p)
}

// Size is how many bytes were hashed.
func (v *Verifier) Size() int64 { return v.n }

// Check fails with ErrUpstream unless the bytes hashed so far have the wanted digest.
func (v *Verifier) Check() error {
	got := Digest(Algorithm + ":" + hex.EncodeToString(v.h.Sum(nil)))
	if got != v.want {
		return fmt.Errorf("content is %s, want %s: %w", got, v.want, errkind.ErrUpstream)
	}
	return nil
}
