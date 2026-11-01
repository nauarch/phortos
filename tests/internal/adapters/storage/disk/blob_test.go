// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk_test

// Verifies:
//   - A blob being written can be read back as far as it is written, and is invisible until committed
//   - A committed blob opens with its size and seeks
//   - An aborted blob leaves nothing behind
//   - Blobs are kept under a name without ':' (SMB refuses it)

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestABlobIsReadableWhileWrittenAndVisibleOnlyOnceCommitted(t *testing.T) {
	s, root := open(t)
	data := []byte("0123456789")
	d := digest.Of(data)
	p, err := s.Begin(d)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write(data[:4])
	buf := make([]byte, 4)
	if n, err := p.ReadAt(buf, 0); n != 4 || string(buf) != "0123" {
		t.Fatalf("read back %d %q %v", n, buf, err)
	}
	if _, _, err := s.Blob(d); errkind.Of(err) != errkind.NotFound {
		t.Fatalf("visible before commit: %v", err)
	}
	_, _ = p.Write(data[4:])
	if err := p.Commit(); err != nil {
		t.Fatal(err)
	}
	body, size, err := s.Blob(d)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if size != int64(len(data)) {
		t.Errorf("size %d", size)
	}
	if _, err := body.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(body); string(rest) != "6789" {
		t.Errorf("after seeking: %q", rest)
	}
	files, _ := filepath.Glob(filepath.Join(root, "blobs", "sha256", "*", "*"))
	if len(files) != 1 || strings.Contains(filepath.Base(files[0]), ":") {
		t.Errorf("blob files %v", files)
	}
}

func TestAnAbortedBlobLeavesNothing(t *testing.T) {
	s, root := open(t)
	d := digest.Of([]byte("x"))
	p, err := s.Begin(d)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte("partial"))
	if err := p.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Blob(d); errkind.Of(err) != errkind.NotFound {
		t.Fatalf("got %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "tmp", "*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}
