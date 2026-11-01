// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk_test

// Verifies:
//   - A release record reads back as written, under any URL (ports and odd characters included)
//   - A URL never recorded is not found
//   - Every record is listed; a torn one is skipped

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAReleaseRecordReadsBack(t *testing.T) {
	s, _ := open(t)
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	r := storage.ReleaseRecord{
		URL:    "http://127.0.0.1:8080/debian/dists/trixie/InRelease",
		Digest: digest.Of([]byte("x")), Fetched: at, Modified: at.Add(-time.Hour), ETag: `"abc"`,
	}
	if err := s.PutRelease(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Release(r.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != r.URL || got.Digest != r.Digest || !got.Fetched.Equal(at) || !got.Modified.Equal(r.Modified) || got.ETag != r.ETag {
		t.Errorf("%+v", got)
	}
	if _, err := s.Release("http://deb.debian.org/debian/dists/trixie/InRelease"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("never recorded: %v", err)
	}
}

func TestEveryReadableRecordIsListed(t *testing.T) {
	s, root := open(t)
	for _, u := range []string{"http://a/dists/x/InRelease", "http://b/flat/InRelease"} {
		if err := s.PutRelease(storage.ReleaseRecord{URL: u, Digest: digest.Of([]byte(u))}); err != nil {
			t.Fatal(err)
		}
	}
	torn := filepath.Join(root, "releases", "00", "torn.json")
	_ = os.MkdirAll(filepath.Dir(torn), 0o750)
	_ = os.WriteFile(torn, []byte("{"), 0o640)
	got, err := s.Releases()
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}
