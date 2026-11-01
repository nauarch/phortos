// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Marker is the file that marks a folder as a phortos store. Without it the store refuses to open,
// so an unmounted share is never filled through its empty mount point.
const Marker = ".phortos-volume"

// Options say where the store is and whether to make it: Init creates the folder and its marker
// when they are missing.
type Options struct {
	Root string
	Init bool
}

// Dir is a store in a folder, on a local disk or a mounted share (NFS, SMB). Names hold no ':'
// (SMB refuses it), and everything is written to tmp/ first and renamed into place, which is
// atomic on both, so a crash never leaves a half-written file that looks complete.
type Dir struct {
	root string
}

var _ storage.Store = (*Dir)(nil)

// Open opens the store at o.Root, refusing a folder without the marker unless o.Init asks to make
// one.
func Open(o Options) (*Dir, error) {
	if o.Root == "" {
		return nil, fmt.Errorf("store: no folder given: %w", errkind.ErrInvalid)
	}
	marker := filepath.Join(o.Root, Marker)
	_, err := os.Stat(marker)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrNotExist) && o.Init:
		if err := os.MkdirAll(o.Root, 0o750); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
		if err := os.WriteFile(marker, []byte("phortos store\n"), 0o640); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("store: %s has no %s; if this is the right folder (and its share is mounted), start once with --init: %w", o.Root, Marker, errkind.ErrInvalid)
	default:
		return nil, fmt.Errorf("store: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(o.Root, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Dir{root: o.Root}, nil
}
