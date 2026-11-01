// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"fmt"
	"os"
	"path/filepath"
)

// temp creates a file in tmp/, on the same filesystem as its final place, so renaming it there
// is atomic.
func (s *Dir) temp() (*os.File, error) {
	f, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "part-*")
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return f, nil
}

// place syncs a finished temp file, closes it and renames it to path.
func place(f *os.File, path string) error {
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("store: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("store: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("store: %w", err)
	}
	return nil
}

// writeFile writes data to path through a temp file.
func (s *Dir) writeFile(path string, data []byte) error {
	f, err := s.temp()
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return fmt.Errorf("store: %w", err)
	}
	return place(f, path)
}
