// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package bytesize

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// units are the suffixes a size may carry, in powers of 1024: 200G is 200 GiB. Both G and GiB (and
// GB) spell it, since a disk's cap is never meant in powers of 1000.
var units = []struct {
	suffixes []string
	factor   int64
}{
	{[]string{"tib", "tb", "t"}, 1 << 40},
	{[]string{"gib", "gb", "g"}, 1 << 30},
	{[]string{"mib", "mb", "m"}, 1 << 20},
	{[]string{"kib", "kb", "k"}, 1 << 10},
	{[]string{"b", ""}, 1},
}

// Parse reads a size such as 200G, 1.5TiB or 512m; "" and "0" are zero.
func Parse(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return 0, nil
	}
	for _, u := range units {
		for _, suffix := range u.suffixes {
			num, ok := strings.CutSuffix(t, suffix)
			if !ok {
				continue
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("size %q: %w", s, errkind.ErrInvalid)
			}
			return int64(n * float64(u.factor)), nil
		}
	}
	return 0, fmt.Errorf("size %q: %w", s, errkind.ErrInvalid)
}

// Format spells a size in the largest unit it fills, to one decimal, such as 1.5GiB or 200GiB.
func Format(n int64) string {
	for _, u := range units[:len(units)-1] {
		if n >= u.factor {
			num := strconv.FormatFloat(float64(n)/float64(u.factor), 'f', 1, 64)
			return strings.TrimSuffix(num, ".0") + strings.ToUpper(u.suffixes[0][:1]) + "iB"
		}
	}
	return strconv.FormatInt(n, 10) + "B"
}
