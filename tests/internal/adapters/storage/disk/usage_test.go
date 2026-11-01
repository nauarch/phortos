// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk_test

// Verifies:
//   - Touching a blob sets the last use the listing reports, with each blob's size
//   - A removed blob is gone; removing or touching a missing one is not found
//   - A healthy store passes its check; one whose marker is gone (a lost share) fails it

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// put commits a blob.
func put(t *testing.T, s *disk.Dir, data []byte) digest.Digest {
	t.Helper()
	d := digest.Of(data)
	p, err := s.Begin(d)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write(data)
	if err := p.Commit(); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTouchingSetsTheLastUseListed(t *testing.T) {
	s, _ := open(t)
	d := put(t, s, []byte("twelve bytes"))
	used := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := s.Touch(d, used); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Blobs()
	if err != nil || len(entries) != 1 {
		t.Fatalf("%v %v", entries, err)
	}
	if e := entries[0]; e.Digest != d || e.Size != 12 || !e.Used.Equal(used) {
		t.Errorf("%+v", e)
	}
}

func TestARemovedBlobIsGone(t *testing.T) {
	s, _ := open(t)
	d := put(t, s, []byte("x"))
	if err := s.RemoveBlob(d); err != nil {
		t.Fatal(err)
	}
	if entries, _ := s.Blobs(); len(entries) != 0 {
		t.Errorf("still listed: %v", entries)
	}
	if err := s.RemoveBlob(d); errkind.Of(err) != errkind.NotFound {
		t.Errorf("removing again: %v", err)
	}
	if err := s.Touch(d, time.Now()); errkind.Of(err) != errkind.NotFound {
		t.Errorf("touching a missing blob: %v", err)
	}
}

func TestALostShareFailsTheCheck(t *testing.T) {
	s, root := open(t)
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, disk.Marker)); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(); err == nil {
		t.Error("a store without its marker passed")
	}
}
