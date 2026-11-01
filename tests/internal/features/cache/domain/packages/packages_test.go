// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package packages_test

// Verifies:
//   - Every stanza naming a file, a SHA256 and a size is found, whatever the order of its fields,
//     with continuation lines ignored and no blank line needed at the end
//   - A stanza missing one of the three is skipped; a malformed sum fails the index

import (
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/features/cache/domain/packages"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

const hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestEveryCompleteStanzaIsFound(t *testing.T) {
	in := "Package: a\nFilename: pool/main/a/a_1_amd64.deb\nSize: 10\nSHA256: " + hexA + "\nDescription: x\n continued\n SHA256: notasum\n\n" +
		"Package: b\nSHA256: " + hexA + "\nSize: 20\n\n" +
		"Package: c\nSize: 30\nSHA256: " + hexA + "\nFilename: ./c_1_all.deb"
	got := map[string]release.Sum{}
	if err := packages.Read(strings.NewReader(in), func(f string, s release.Sum) { got[f] = s }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["pool/main/a/a_1_amd64.deb"].Size != 10 || got["./c_1_all.deb"].Size != 30 {
		t.Errorf("%+v", got)
	}
}

func TestAMalformedSumFailsTheIndex(t *testing.T) {
	in := "Package: a\nFilename: a.deb\nSize: 10\nSHA256: nothex\n"
	if err := packages.Read(strings.NewReader(in), func(string, release.Sum) {}); errkind.Of(err) != errkind.Invalid {
		t.Error(err)
	}
}
