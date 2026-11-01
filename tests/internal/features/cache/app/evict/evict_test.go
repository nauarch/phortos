// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package evict_test

// Verifies:
//   - A pass over a store beyond its cap removes the least recently used blobs and counts them
//   - A pass measures the store even without a cap, and removes nothing

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/features/cache/app/evict"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// fill stores blobs of 100 bytes each, used in the order given (the first the longest ago).
func fill(t *testing.T, s *disk.Dir, names ...string) []digest.Digest {
	t.Helper()
	at := time.Date(2026, 10, 29, 5, 0, 0, 0, time.UTC)
	var out []digest.Digest
	for i, name := range names {
		data := bytes.Repeat([]byte(name[:1]), 100)
		d := digest.Of(data)
		p, err := s.Begin(d)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.Write(data)
		if err := p.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := s.Touch(d, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// open is a fresh store.
func open(t *testing.T) *disk.Dir {
	t.Helper()
	s, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAPassBeyondTheCapRemovesTheLeastRecentlyUsed(t *testing.T) {
	s := open(t)
	ds := fill(t, s, "a", "b", "c", "d")
	e := &evict.Evictor{Store: s, Log: slog.New(slog.DiscardHandler)}
	e.Max.Store(300)
	if err := e.Pass(); err != nil {
		t.Fatal(err)
	}
	for i, d := range ds {
		body, _, err := s.Blob(d)
		if err == nil {
			_ = body.Close()
		}
		if gone := errkind.Of(err) == errkind.NotFound; gone != (i < 2) {
			t.Errorf("blob %d: gone %v", i, gone)
		}
	}
	if e.Evicted.Load() != 2 || e.EvictedBytes.Load() != 200 || e.StoreBytes.Load() != 200 || e.StoreBlobs.Load() != 2 {
		t.Errorf("evicted %d (%d bytes), store %d bytes in %d", e.Evicted.Load(), e.EvictedBytes.Load(), e.StoreBytes.Load(), e.StoreBlobs.Load())
	}
}

func TestAPassWithoutACapOnlyMeasures(t *testing.T) {
	s := open(t)
	fill(t, s, "a", "b", "c")
	e := &evict.Evictor{Store: s, Log: slog.New(slog.DiscardHandler)}
	if err := e.Pass(); err != nil {
		t.Fatal(err)
	}
	if e.Evicted.Load() != 0 || e.StoreBytes.Load() != 300 || e.StoreBlobs.Load() != 3 {
		t.Errorf("evicted %d, store %d bytes in %d", e.Evicted.Load(), e.StoreBytes.Load(), e.StoreBlobs.Load())
	}
}
