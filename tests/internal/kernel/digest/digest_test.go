// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package digest_test

// Verifies:
//   - A sha256 digest with 64 lower-case hex digits is read; anything else is invalid
//   - The digest of bytes is their sha256, and its hex part is the digits alone

import (
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// empty is the sha256 of no bytes.
const empty = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestOnlyASha256DigestIsRead(t *testing.T) {
	if d, err := digest.Parse(empty); err != nil || d.String() != empty {
		t.Fatalf("%s = %q, %v", empty, d, err)
	}
	for _, bad := range []string{
		"", "sha256:", "sha512:" + strings.Repeat("a", 128), "sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("A", 64), "sha256:" + strings.Repeat("g", 64), strings.Repeat("a", 64),
	} {
		if _, err := digest.Parse(bad); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%q: %v, want invalid", bad, err)
		}
	}
}

func TestTheDigestOfBytesIsTheirSha256(t *testing.T) {
	d := digest.Of(nil)
	if d.String() != empty {
		t.Fatalf("got %s", d)
	}
	if d.Hex() != strings.TrimPrefix(empty, "sha256:") {
		t.Errorf("hex %s", d.Hex())
	}
}
