// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/bytesize"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// chunk is how much of a file is copied from upstream at a time.
const chunk = 256 << 10

// flight is one file download that every concurrent request for that file shares. started
// closes once upstream has answered: with startErr, or with the size and the pending file being
// written. From then on written counts the bytes on disk, and done says the download has landed
// (committed, or failed with err).
type flight struct {
	started  chan struct{}
	startErr error
	size     int64

	mu      sync.Mutex
	more    *sync.Cond
	pending storage.Pending
	written int64
	done    bool
	err     error
}

// newFlight is a download not yet answered.
func newFlight() *flight {
	f := &flight{started: make(chan struct{})}
	f.more = sync.NewCond(&f.mu)
	return f
}

// fail ends a download upstream refused or the store could not begin.
func (f *flight) fail(err error) {
	f.startErr = err
	f.mu.Lock()
	f.done, f.err = true, err
	f.mu.Unlock()
	close(f.started)
}

// begin lets the requests waiting on the download read it.
func (f *flight) begin(size int64, p storage.Pending) {
	f.size, f.pending = size, p
	close(f.started)
}

// fill copies body into the pending file, waking readers after each chunk, and checks that what
// arrived has the digest and size it should. A body that delivers nothing for stallAfter, or (with
// minRate above 0) less than minRate bytes a second over a whole stallAfter, is closed and the
// download fails, so a stuck or crawling upstream never holds requests for long: the connection
// closes with it, and the next download of the file opens a fresh one. Bytes read count into
// fetched.
func (f *flight) fill(body io.ReadCloser, want digest.Digest, stallAfter time.Duration, minRate int64, fetched *atomic.Int64) error {
	var stalled atomic.Bool
	watchdog := time.AfterFunc(stallAfter, func() {
		stalled.Store(true)
		_ = body.Close()
	})
	defer watchdog.Stop()
	var got atomic.Int64
	slow := make(chan int64, 1)
	if minRate > 0 {
		done := make(chan struct{})
		defer close(done)
		go pace(body, &got, int64(float64(minRate)*stallAfter.Seconds()), stallAfter, done, slow)
	}
	v := digest.NewVerifier(want)
	buf := make([]byte, chunk)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			watchdog.Reset(stallAfter)
			got.Add(int64(n))
			fetched.Add(int64(n))
			if _, err := f.pending.Write(buf[:n]); err != nil {
				return fmt.Errorf("writing %s: %w", want, err)
			}
			_, _ = v.Write(buf[:n])
			f.mu.Lock()
			f.written += int64(n)
			f.more.Broadcast()
			f.mu.Unlock()
		}
		if rerr == io.EOF && !stalled.Load() {
			break
		}
		if stalled.Load() {
			return fmt.Errorf("%s: upstream sent nothing for %s: %w", want, stallAfter, errkind.ErrUnreachable)
		}
		if rerr != nil {
			select {
			case n := <-slow:
				return fmt.Errorf("%s: upstream sent %s in %s, below %s a second: %w", want, bytesize.Format(n), stallAfter, bytesize.Format(minRate), errkind.ErrUnreachable)
			default:
			}
			return fmt.Errorf("reading %s from upstream: %w: %w", want, errkind.ErrUnreachable, rerr)
		}
	}
	if f.size >= 0 && v.Size() != f.size {
		return fmt.Errorf("%s: %d bytes arrived, %d expected: %w", want, v.Size(), f.size, errkind.ErrUpstream)
	}
	return v.Check()
}

// pace closes body when a window passes in which fewer than floor bytes arrived (got counts them),
// telling slow how many did; it stops when done closes.
func pace(body io.Closer, got *atomic.Int64, floor int64, window time.Duration, done <-chan struct{}, slow chan<- int64) {
	tick := time.NewTicker(window)
	defer tick.Stop()
	var last int64
	for {
		select {
		case <-done:
			return
		case <-tick.C:
		}
		n := got.Load()
		if n-last < floor {
			slow <- n - last
			_ = body.Close()
			return
		}
		last = n
	}
}

// finish lands the download: commits the file when err is nil, throws it away otherwise, and
// wakes every reader.
func (f *flight) finish(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		err = f.pending.Commit()
	} else {
		_ = f.pending.Abort()
	}
	f.done, f.err = true, err
	f.more.Broadcast()
}
