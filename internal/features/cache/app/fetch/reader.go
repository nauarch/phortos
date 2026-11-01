// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"errors"
	"io"
	"sync/atomic"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/digest"
)

// flightReader is one request's view of a shared download: it reads the pending file as far as
// it has arrived, waits for more, and once the download has landed carries on from the committed
// file in the store.
type flightReader struct {
	f      *flight
	store  storage.Store
	digest digest.Digest
	off    int64
	landed io.ReadSeekCloser
	served *atomic.Int64
}

// Read reads the next bytes of the file, waiting for them to arrive; a failed download fails it.
// What it reads counts as served.
func (r *flightReader) Read(p []byte) (int, error) {
	n, err := r.read(p)
	r.served.Add(int64(n))
	return n, err
}

// read reads without counting.
func (r *flightReader) read(p []byte) (int, error) {
	if r.landed != nil {
		return r.landed.Read(p)
	}
	f := r.f
	f.mu.Lock()
	for r.off >= f.written && !f.done {
		f.more.Wait()
	}
	switch {
	case !f.done:
		n := int(min(int64(len(p)), f.written-r.off))
		n, err := f.pending.ReadAt(p[:n], r.off)
		f.mu.Unlock()
		r.off += int64(n)
		if errors.Is(err, io.EOF) && n > 0 {
			err = nil
		}
		return n, err
	case f.err != nil:
		err := f.err
		f.mu.Unlock()
		return 0, err
	default:
		f.mu.Unlock()
		return r.fromStore(p)
	}
}

// fromStore switches to the committed file at the reader's offset and reads from it.
func (r *flightReader) fromStore(p []byte) (int, error) {
	body, _, err := r.store.Blob(r.digest)
	if err != nil {
		return 0, err
	}
	if _, err := body.Seek(r.off, io.SeekStart); err != nil {
		_ = body.Close()
		return 0, err
	}
	r.landed = body
	return body.Read(p)
}

// Close lets go of the committed file, if the reader got that far; the download itself goes on.
func (r *flightReader) Close() error {
	if r.landed != nil {
		return r.landed.Close()
	}
	return nil
}
