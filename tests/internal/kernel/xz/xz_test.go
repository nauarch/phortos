// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package xz_test

// Verifies, against files the real xz tool (XZ Utils 5.8.1) made, listed in testdata/SHA256SUMS
// with the SHA256 and size of what each holds:
//   - Every file decodes to exactly its original: every check type, presets -0 to -9e, unusual
//     lc/lp/pb, incompressible data (stored chunks), several blocks, two streams with padding
//     between them, and an empty file
//   - Any flipped byte fails as invalid (a check, a checksum or the data itself), and any cut
//     fails too, without a panic or a hang
//   - Data that is not xz is invalid

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/xz"
)

// fixture is one test file and what it decodes to.
type fixture struct {
	name string
	sum  string
	size int
}

// fixtures reads testdata/SHA256SUMS.
func fixtures(t *testing.T) []fixture {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []fixture
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		n, _ := strconv.Atoi(fields[1])
		out = append(out, fixture{name: fields[2], sum: fields[0], size: n})
	}
	if len(out) < 9 {
		t.Fatalf("only %d fixtures", len(out))
	}
	return out
}

// read is a fixture's bytes.
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEveryFixtureDecodesToItsOriginal(t *testing.T) {
	for _, f := range fixtures(t) {
		var out bytes.Buffer
		if err := xz.Decode(&out, bytes.NewReader(read(t, f.name))); err != nil {
			t.Errorf("%s: %v", f.name, err)
			continue
		}
		sum := sha256.Sum256(out.Bytes())
		if out.Len() != f.size || hex.EncodeToString(sum[:]) != f.sum {
			t.Errorf("%s: %d bytes, sha256 %x, want %d bytes, %s", f.name, out.Len(), sum, f.size, f.sum)
		}
	}
}

func TestDamageFailsWithoutPanicOrHang(t *testing.T) {
	data := read(t, "packages-sha256-9e.xz")
	for i := 0; i < len(data); i += max(1, len(data)/400) {
		start := time.Now()
		flipped := bytes.Clone(data)
		flipped[i] ^= 0x55
		var out bytes.Buffer
		if err := xz.Decode(&out, bytes.NewReader(flipped)); errkind.Of(err) != errkind.Invalid {
			t.Fatalf("byte %d flipped: %v", i, err)
		}
		if err := xz.Decode(&out, bytes.NewReader(data[:i])); errkind.Of(err) != errkind.Invalid {
			t.Fatalf("cut at %d: %v", i, err)
		}
		if took := time.Since(start); took > 10*time.Second {
			t.Fatalf("damage at %d took %s to be refused", i, took)
		}
	}
}

func TestDataThatIsNotXZIsInvalid(t *testing.T) {
	var out bytes.Buffer
	if err := xz.Decode(&out, strings.NewReader("\x1f\x8b gzip, not xz")); errkind.Of(err) != errkind.Invalid {
		t.Error(err)
	}
}
