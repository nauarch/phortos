// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Files hands out files whose SHA256 is known: from the store, or from one download that every
// concurrent request for the same file shares, checked against the digest (and the size, when
// known) before it is kept. Downloads run on Background, not on the request that started them,
// so a file whose first requester goes away still lands for the next; canceling Background stops
// them all. A download that receives nothing for StallAfter, or less than MinRate bytes a second
// over a whole StallAfter (0 for no such floor), is given up.
type Files struct {
	Store      storage.Store
	Upstream   upstream.Upstream
	Background context.Context
	Clock      clock.Clock
	StallAfter time.Duration
	MinRate    int64
	Counters   *Counters
	Log        *slog.Logger

	mu      sync.Mutex
	flights map[digest.Digest]*flight
	// prefetched are files downloaded ahead of a request a client had queued, until it comes
	// (forgotten after prefetchKept): that request counts as the miss it was.
	prefetched map[digest.Digest]time.Time
}

// prefetchKept is how long a prefetched file waits to be asked for before its request would
// count as a hit; prefetchSweep is how many marks there may be before old ones are swept.
const (
	prefetchKept  = 10 * time.Minute
	prefetchSweep = 256
)

// Prefetch starts downloading a file (digest d, size -1 when unknown) from url ahead of a request
// for it, unless it is stored or on its way already, and says whether it did. It counts as no
// request; the request that comes for it counts as a miss.
func (f *Files) Prefetch(url string, d digest.Digest, size int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.flights[d]; ok {
		return false
	}
	if body, _, err := f.Store.Blob(d); err == nil {
		_ = body.Close()
		return false
	} else if errkind.Of(err) != errkind.NotFound {
		return false
	}
	now := f.Clock.Now()
	if f.flights == nil {
		f.flights = map[digest.Digest]*flight{}
	}
	if f.prefetched == nil {
		f.prefetched = map[digest.Digest]time.Time{}
	}
	if len(f.prefetched) >= prefetchSweep {
		for k, at := range f.prefetched {
			if now.Sub(at) > prefetchKept {
				delete(f.prefetched, k)
			}
		}
	}
	f.prefetched[d] = now
	f.Counters.Prefetched.Add(1)
	fl := newFlight()
	f.flights[d] = fl
	go f.download(fl, url, d, size)
	return true
}

// asked counts a request for d as source, or as a miss when d was prefetched for it.
func (f *Files) asked(d digest.Digest, source repo.Source, n *atomic.Int64) repo.Source {
	if at, ok := f.prefetched[d]; ok {
		delete(f.prefetched, d)
		if f.Clock.Now().Sub(at) <= prefetchKept {
			f.Counters.Misses.Add(1)
			return repo.Miss
		}
	}
	n.Add(1)
	return source
}

// Get is the file with digest d and size (-1 when unknown): from the store when it is there, else
// joined to its download from url, which it starts when none runs. It returns once upstream has
// answered.
func (f *Files) Get(ctx context.Context, url string, d digest.Digest, size int64) (repo.File, error) {
	fl, file, err := f.flightFor(url, d, size)
	if fl == nil {
		return file, err
	}
	select {
	case <-fl.started:
	case <-ctx.Done():
		return repo.File{}, ctx.Err()
	}
	if fl.startErr != nil {
		return repo.File{}, fl.startErr
	}
	return repo.File{
		Status: http.StatusOK, Size: fl.size, Source: file.Source,
		Body: &flightReader{f: fl, store: f.Store, digest: d, served: &f.Counters.BytesServed},
	}, nil
}

// flightFor finds the running download of a file, or the stored file, or starts the download.
// The store is looked at under the same lock that lands a download, so a file is never missed
// between the two.
func (f *Files) flightFor(url string, d digest.Digest, size int64) (*flight, repo.File, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fl, ok := f.flights[d]; ok {
		return fl, repo.File{Source: f.asked(d, repo.Joined, &f.Counters.Joins)}, nil
	}
	body, n, err := f.Store.Blob(d)
	switch {
	case err == nil:
		source := f.asked(d, repo.Hit, &f.Counters.Hits)
		if terr := f.Store.Touch(d, f.Clock.Now()); terr != nil {
			f.Log.Debug("file use not recorded", "digest", d, "err", terr)
		}
		return nil, repo.File{Status: http.StatusOK, Size: n, Source: source, Body: countedSeeker{ReadSeekCloser: body, n: &f.Counters.BytesServed}}, nil
	case errkind.Of(err) != errkind.NotFound:
		return nil, repo.File{}, err
	}
	if f.flights == nil {
		f.flights = map[digest.Digest]*flight{}
	}
	delete(f.prefetched, d)
	f.Counters.Misses.Add(1)
	fl := newFlight()
	f.flights[d] = fl
	go f.download(fl, url, d, size)
	return fl, repo.File{Source: repo.Miss}, nil
}

// download fetches a file into the store for its flight, then lets the flight go.
func (f *Files) download(fl *flight, url string, d digest.Digest, size int64) {
	defer f.land(d)
	resp, err := f.Upstream.Fetch(f.Background, url, upstream.Conditions{})
	if err == nil && resp.Status != http.StatusOK {
		_ = resp.Body.Close()
		err = statusError(url, resp.Status)
	}
	if err == nil && size >= 0 && resp.Size >= 0 && resp.Size != size {
		_ = resp.Body.Close()
		err = fmt.Errorf("%s: upstream announces %d bytes, the index says %d: %w", url, resp.Size, size, errkind.ErrUpstream)
	}
	if err != nil {
		f.Counters.Failures.Add(1)
		fl.fail(err)
		f.Log.Warn("file not fetched", "url", url, "err", err)
		return
	}
	defer resp.Body.Close()
	if size < 0 {
		size = resp.Size
	}
	pending, err := f.Store.Begin(d)
	if err != nil {
		f.Counters.Failures.Add(1)
		fl.fail(err)
		f.Log.Error("file not stored", "digest", d, "err", err)
		return
	}
	fl.begin(size, pending)
	err = fl.fill(resp.Body, d, f.StallAfter, f.MinRate, &f.Counters.BytesFetched)
	fl.finish(err)
	if err != nil {
		f.Counters.Failures.Add(1)
		f.Log.Warn("download failed", "url", url, "err", err)
		return
	}
	f.Log.Info("file cached", "url", url, "size", size)
}

// land forgets a finished download; later requests find the file in the store.
func (f *Files) land(d digest.Digest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.flights, d)
}

// statusError is the failure an unexpected upstream status stands for.
func statusError(url string, status int) error {
	if status == http.StatusNotFound || status == http.StatusGone {
		return fmt.Errorf("%s: upstream answered %d: %w", url, status, errkind.ErrNotFound)
	}
	return fmt.Errorf("%s: upstream answered %d: %w", url, status, errkind.ErrUpstream)
}
