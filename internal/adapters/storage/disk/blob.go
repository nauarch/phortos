// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Blob opens a committed blob.
func (s *Dir) Blob(d digest.Digest) (io.ReadSeekCloser, int64, error) {
	f, err := os.Open(s.blobPath(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, fmt.Errorf("blob %s: %w", d, errkind.ErrNotFound)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("store: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("store: %w", err)
	}
	return f, info.Size(), nil
}

// Begin starts writing a blob in tmp/.
func (s *Dir) Begin(d digest.Digest) (storage.Pending, error) {
	f, err := s.temp()
	if err != nil {
		return nil, err
	}
	return &pending{file: f, path: s.blobPath(d)}, nil
}

// pending is a blob being written to a temp file, readable while it grows.
type pending struct {
	file *os.File
	path string
}

// Write appends to the blob.
func (p *pending) Write(b []byte) (int, error) { return p.file.Write(b) }

// ReadAt reads what was written, without moving where writing goes on.
func (p *pending) ReadAt(b []byte, off int64) (int, error) { return p.file.ReadAt(b, off) }

// Commit syncs the blob and renames it into place.
func (p *pending) Commit() error { return place(p.file, p.path) }

// Abort throws the blob away.
func (p *pending) Abort() error {
	_ = p.file.Close()
	return os.Remove(p.file.Name())
}
