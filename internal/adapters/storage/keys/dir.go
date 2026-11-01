// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package keys keeps phortos' private keys in a folder on the local disk: the folder is made
// readable by its owner only, every file is written whole (through a temporary file and a rename)
// with mode 0600, and on Unix a key file others could read is refused rather than used.
package keys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	contract "github.com/nauarch/phortos/internal/contracts/keys"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Dir is a key folder.
type Dir struct {
	root string
}

var _ contract.Store = (*Dir)(nil)

// Open opens (and makes, when missing) the key folder at root.
func Open(root string) (*Dir, error) {
	if root == "" {
		return nil, fmt.Errorf("keys: no folder given: %w", errkind.ErrInvalid)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	return &Dir{root: root}, nil
}

// Load reads a key file, refusing one whose mode lets others read it.
func (d *Dir) Load(name string) ([]byte, error) {
	p, err := d.path(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("keys: %s: %w", name, errkind.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("keys: %s has mode %v, others may read it; make it 0600: %w", p, info.Mode().Perm(), errkind.ErrInvalid)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	return data, nil
}

// Save writes a key file whole with mode 0600.
func (d *Dir) Save(name string, data []byte) error {
	p, err := d.path(name)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(d.root, ".tmp-*")
	if err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = f.Close()
		return fmt.Errorf("keys: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("keys: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("keys: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return fmt.Errorf("keys: %w", err)
	}
	return nil
}

// path is where a named file lives; names are plain file names.
func (d *Dir) path(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name[0] == '.' {
		return "", fmt.Errorf("keys: file name %q: %w", name, errkind.ErrInvalid)
	}
	return filepath.Join(d.root, name), nil
}
