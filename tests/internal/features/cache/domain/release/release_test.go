// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package release_test

// Verifies:
//   - An inline-signed release file gives the SHA256 and size of every listed file, read from
//     the signed text only, with dash escapes undone and the by-hash flag
//   - An unsigned Release reads the same way
//   - A file listed by SHA512 only has its SHA512 taken; listed by both, its SHA256
//   - Paths climbing out of the release folder, malformed sums and files listing no SHA256 or
//     SHA512 are invalid
//   - Only 64 lower-case hex digits count as a SHA256

import (
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestASignedReleaseListsItsSums(t *testing.T) {
	in := "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" +
		"Origin: Debian\nAcquire-By-Hash: yes\nMD5Sum:\n 0123 99 main/binary-amd64/Packages.gz\n" +
		"SHA256:\n " + hexA + " 1234 main/binary-amd64/Packages.gz\n " + hexB + "  56 ./Packages\n" +
		"- Note: a dash-escaped line\n" +
		"-----BEGIN PGP SIGNATURE-----\n\n" + " " + hexA + " 1 forged/in/the/signature\n-----END PGP SIGNATURE-----\n"
	r, err := release.Parse([]byte(strings.ReplaceAll(in, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !r.ByHash || len(r.Files) != 2 {
		t.Fatalf("%+v", r)
	}
	if got := r.Files["main/binary-amd64/Packages.gz"]; got != (release.Sum{Hex: hexA, Size: 1234}) {
		t.Errorf("Packages.gz: %+v", got)
	}
	if got := r.Files["Packages"]; got != (release.Sum{Hex: hexB, Size: 56}) {
		t.Errorf("flat Packages: %+v", got)
	}
}

func TestAnUnsignedReleaseReadsTheSame(t *testing.T) {
	r, err := release.Parse([]byte("Origin: NVIDIA\nSHA256:\n " + hexA + " 7 Packages\nAcquire-By-Hash: no\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.ByHash || r.Files["Packages"].Size != 7 {
		t.Errorf("%+v", r)
	}
}

func TestASHA512IsTakenWhereNoSHA256IsListed(t *testing.T) {
	r, err := release.Parse([]byte("Origin: NVIDIA\nSHA512:\n " + hexA + hexB + " 7 Packages\n " + hexB + hexA + " 3 Packages.xz\nSHA256:\n " + hexA + " 3 Packages.xz\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Files["Packages"]; got != (release.Sum{Hex: hexA + hexB, Size: 7, SHA512: true}) {
		t.Errorf("SHA512 only: %+v", got)
	}
	if got := r.Files["Packages.xz"]; got != (release.Sum{Hex: hexA, Size: 3}) {
		t.Errorf("both listed, SHA256 wins: %+v", got)
	}
	if _, err := release.Parse([]byte("SHA512:\n " + hexA + " 7 Packages\n")); errkind.Of(err) != errkind.Invalid {
		t.Errorf("a SHA256-long SHA512: %v", err)
	}
}

func TestBadReleasesAreInvalid(t *testing.T) {
	for name, in := range map[string]string{
		"climbs out":  "SHA256:\n " + hexA + " 7 ../../etc/passwd\n",
		"absolute":    "SHA256:\n " + hexA + " 7 /etc/passwd\n",
		"short sum":   "SHA256:\n abc 7 Packages\n",
		"bad size":    "SHA256:\n " + hexA + " -7 Packages\n",
		"two fields":  "SHA256:\n " + hexA + " Packages\n",
		"no SHA256":   "Origin: x\nMD5Sum:\n 0123 7 Packages\n",
		"empty input": "",
	} {
		if _, err := release.Parse([]byte(in)); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOnlyLowerCaseHexIsASum(t *testing.T) {
	if !release.IsHex(hexA) || release.IsHex(strings.ToUpper(hexA)) || release.IsHex(hexA[1:]) || release.IsHex(hexA[1:]+"g") {
		t.Error("IsHex is wrong")
	}
}
