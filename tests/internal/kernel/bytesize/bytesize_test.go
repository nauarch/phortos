// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package bytesize_test

// Verifies:
//   - Sizes read in powers of 1024 with any spelling of their unit; empty is zero
//   - A malformed or negative size is invalid
//   - A size spells itself in its largest unit, to one decimal

import (
	"testing"

	"github.com/nauarch/phortos/internal/kernel/bytesize"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestSizesReadInPowersOf1024(t *testing.T) {
	for in, want := range map[string]int64{
		"": 0, "0": 0, "512": 512, "1k": 1 << 10, "200G": 200 << 30, "200GiB": 200 << 30, "200gb": 200 << 30,
		"1.5T": 3 << 39, " 64 MiB ": 64 << 20,
	} {
		if got, err := bytesize.Parse(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"lots", "-1G", "1.2.3G", "G"} {
		if _, err := bytesize.Parse(bad); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestASizeSpellsItselfInItsLargestUnit(t *testing.T) {
	for in, want := range map[int64]string{512: "512B", 2048: "2KiB", 200 << 30: "200GiB", 3 << 39: "1.5TiB", 1536 << 20: "1.5GiB"} {
		if got := bytesize.Format(in); got != want {
			t.Errorf("%d = %s, want %s", in, got, want)
		}
	}
}
