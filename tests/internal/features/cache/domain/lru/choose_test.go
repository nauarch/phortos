// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package lru_test

// Verifies:
//   - A store within its cap, or without one, loses nothing
//   - Over its cap, the least recently used blobs go first, until nine tenths of the cap is left

import (
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/features/cache/domain/lru"
	"github.com/nauarch/phortos/internal/kernel/digest"
)

// entry is a blob of size last used minutesAgo before a fixed moment.
func entry(name string, size int64, minutesAgo int) storage.Entry {
	at := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC).Add(-time.Duration(minutesAgo) * time.Minute)
	return storage.Entry{Digest: digest.Of([]byte(name)), Size: size, Used: at}
}

func TestAStoreWithinItsCapLosesNothing(t *testing.T) {
	entries := []storage.Entry{entry("a", 40, 1), entry("b", 60, 2)}
	if got := lru.Choose(entries, 100); got != nil {
		t.Errorf("within the cap: %v", got)
	}
	if got := lru.Choose(entries, 0); got != nil {
		t.Errorf("no cap: %v", got)
	}
}

func TestTheLeastRecentlyUsedGoFirst(t *testing.T) {
	newest, middle, oldest, older := entry("new", 30, 1), entry("mid", 30, 10), entry("old", 30, 100), entry("older", 30, 50)
	got := lru.Choose([]storage.Entry{newest, middle, oldest, older}, 80)
	if len(got) != 2 || got[0].Digest != oldest.Digest || got[1].Digest != older.Digest {
		t.Fatalf("evicted %v", got)
	}
}
