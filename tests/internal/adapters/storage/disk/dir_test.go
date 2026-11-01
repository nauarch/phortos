// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk_test

// Verifies:
//   - A folder without the marker is refused, so an unmounted share is never filled
//   - Init makes the folder and its marker; afterwards the folder opens without it
//   - No folder at all is invalid

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAFolderWithoutTheMarkerIsRefused(t *testing.T) {
	root := t.TempDir()
	if _, err := disk.Open(disk.Options{Root: root}); errkind.Of(err) != errkind.Invalid {
		t.Fatalf("got %v, want invalid", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("a refused folder was written to: %v", entries)
	}
}

func TestInitMakesTheStoreOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	if _, err := disk.Open(disk.Options{Root: root, Init: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, disk.Marker)); err != nil {
		t.Fatalf("no marker: %v", err)
	}
	if _, err := disk.Open(disk.Options{Root: root}); err != nil {
		t.Fatalf("reopening: %v", err)
	}
}

func TestNoFolderIsInvalid(t *testing.T) {
	if _, err := disk.Open(disk.Options{}); errkind.Of(err) != errkind.Invalid {
		t.Fatalf("got %v", err)
	}
}

// open is a fresh store in a temporary folder, and that folder.
func open(t *testing.T) (*disk.Dir, string) {
	t.Helper()
	root := t.TempDir()
	s, err := disk.Open(disk.Options{Root: root, Init: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}
