// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package evict

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/features/cache/domain/lru"
	"github.com/nauarch/phortos/internal/kernel/bytesize"
)

// Evictor keeps the store under its cap (Max bytes; 0 for none, changeable while it runs) by
// removing the least recently used blobs, and measures the store as it goes. A blob evicted while
// a pull still reads it is fetched again by the next pull that wants it.
type Evictor struct {
	Store storage.Store
	Max   atomic.Int64
	Log   *slog.Logger

	Evicted, EvictedBytes, StoreBytes, StoreBlobs atomic.Int64
}

// Run makes a pass now and then every interval, until ctx ends.
func (e *Evictor) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := e.Pass(); err != nil {
			e.Log.Error("eviction pass failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Pass measures the store and, when it is over its cap, evicts the least recently used blobs.
func (e *Evictor) Pass() error {
	entries, err := e.Store.Blobs()
	if err != nil {
		return err
	}
	var total int64
	for _, en := range entries {
		total += en.Size
	}
	count := int64(len(entries))
	max := e.Max.Load()
	for _, victim := range lru.Choose(entries, max) {
		if err := e.Store.RemoveBlob(victim.Digest); err != nil {
			e.Log.Warn("blob not evicted", "digest", victim.Digest, "err", err)
			continue
		}
		total -= victim.Size
		count--
		e.Evicted.Add(1)
		e.EvictedBytes.Add(victim.Size)
		e.Log.Info("blob evicted", "digest", victim.Digest, "size", victim.Size, "used", victim.Used)
	}
	e.StoreBytes.Store(total)
	e.StoreBlobs.Store(count)
	if max > 0 && total > max {
		e.Log.Warn("store still over its cap", "size", bytesize.Format(total), "cap", bytesize.Format(max))
	}
	return nil
}
