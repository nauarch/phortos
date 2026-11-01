// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package digest_test

// Verifies:
//   - Bytes written in pieces pass the check of their own digest and count their size
//   - Other bytes fail it as an upstream failure

import (
	"testing"

	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestStreamedBytesAreCheckedAgainstTheirDigest(t *testing.T) {
	data := []byte("layer bytes arriving in pieces")
	v := digest.NewVerifier(digest.Of(data))
	_, _ = v.Write(data[:7])
	_, _ = v.Write(data[7:])
	if err := v.Check(); err != nil {
		t.Fatal(err)
	}
	if v.Size() != int64(len(data)) {
		t.Errorf("size %d", v.Size())
	}
}

func TestOtherBytesFailTheCheck(t *testing.T) {
	v := digest.NewVerifier(digest.Of([]byte("wanted")))
	_, _ = v.Write([]byte("tampered"))
	if err := v.Check(); errkind.Of(err) != errkind.Upstream {
		t.Fatalf("got %v, want an upstream failure", err)
	}
}
