// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package lru

import (
	"slices"

	"github.com/nauarch/phortos/internal/contracts/storage"
)

// lowWater is the share of the cap eviction brings the store down to, so it does not run again on
// the next blob.
const lowWater = 0.9

// Choose picks the blobs to evict from a store over its cap: the least recently used first, until
// what is left fits in nine tenths of the cap. A store within its cap, or one without a cap (max 0),
// gives none.
func Choose(entries []storage.Entry, max int64) []storage.Entry {
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	if max <= 0 || total <= max {
		return nil
	}
	target := int64(float64(max) * lowWater)
	oldest := slices.Clone(entries)
	slices.SortStableFunc(oldest, func(a, b storage.Entry) int { return a.Used.Compare(b.Used) })
	var out []storage.Entry
	for _, e := range oldest {
		if total <= target {
			break
		}
		out = append(out, e)
		total -= e.Size
	}
	return out
}
