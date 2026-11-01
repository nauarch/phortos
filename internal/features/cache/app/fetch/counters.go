// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"io"
	"sync/atomic"
)

// Counters count what the fetchers did since phortos started; the cache reports them as its
// stats.
type Counters struct {
	Hits, Joins, Misses, Failures            atomic.Int64
	ReleasesFresh, ReleasesChecked           atomic.Int64
	ReleasesChanged, ReleasesStale           atomic.Int64
	Passed, PackagesRead, PackagesReadFailed atomic.Int64
	BytesServed, BytesFetched, BytesPassed   atomic.Int64
	// Prefetched are downloads started ahead of a request a client had queued (see
	// Files.Prefetch).
	Prefetched atomic.Int64
}

// counted is a body whose reads add to a counter.
type counted struct {
	io.ReadCloser
	n *atomic.Int64
}

// Read reads and counts.
func (c counted) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// countedSeeker is a stored file whose reads add to a counter; it still seeks, so ranges can be
// served.
type countedSeeker struct {
	io.ReadSeekCloser
	n *atomic.Int64
}

// Read reads and counts.
func (c countedSeeker) Read(p []byte) (int, error) {
	n, err := c.ReadSeekCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}
