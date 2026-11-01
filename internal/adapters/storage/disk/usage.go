// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Touch records a blob's last use as its modification time, which a share keeps faithfully (unlike
// access times, which NFS often drops). It also survives restarts with nothing else to keep.
func (s *Dir) Touch(d digest.Digest, now time.Time) error {
	err := os.Chtimes(s.blobPath(d), now, now)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob %s: %w", d, errkind.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

// Blobs lists every committed blob with its size and last use; files that are not blobs are left
// out.
func (s *Dir) Blobs() ([]storage.Entry, error) {
	var out []storage.Entry
	root := filepath.Join(s.root, "blobs", digest.Algorithm)
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return filepath.SkipDir
		}
		if err != nil || e.IsDir() {
			return err
		}
		d, perr := digest.Parse(digest.Algorithm + ":" + e.Name())
		if perr != nil {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		out = append(out, storage.Entry{Digest: d, Size: info.Size(), Used: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return out, nil
}

// RemoveBlob deletes a committed blob; one already gone is not found.
func (s *Dir) RemoveBlob(d digest.Digest) error {
	err := os.Remove(s.blobPath(d))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob %s: %w", d, errkind.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

// Check fails unless the marker is there and a file can be written, which catches a share that
// went away or turned read-only.
func (s *Dir) Check() error {
	if _, err := os.Stat(filepath.Join(s.root, Marker)); err != nil {
		return fmt.Errorf("store: marker unreadable (is the share mounted?): %w", err)
	}
	f, err := s.temp()
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Remove(f.Name())
}
