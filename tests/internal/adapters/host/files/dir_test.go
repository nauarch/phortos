// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package files_test

// Verifies:
//   - Host paths live under the root; writing makes the folders, replaces, sets the mode and
//     leaves no temporary file
//   - A missing file or folder is NotFound
//   - Remove takes a folder whole, never the root itself, and a missing path is no error

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/host/files"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestFilesAreWrittenAndReadUnderTheRoot(t *testing.T) {
	d := files.Dir{Root: t.TempDir()}
	const path = "/usr/local/lib/phortos/detect"
	for _, data := range []string{"a", "b"} {
		if err := d.Write(path, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := d.Read(path); err != nil || string(got) != "b" {
		t.Fatalf("%q %v", got, err)
	}
	names, _ := d.List("/usr/local/lib/phortos")
	if !slices.Equal(names, []string{"detect"}) {
		t.Errorf("left behind: %v", names)
	}
	info, _ := os.Stat(filepath.Join(d.Root, "usr", "local", "lib", "phortos", "detect"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", info.Mode())
	}
}

func TestMissingIsNotFound(t *testing.T) {
	d := files.Dir{Root: t.TempDir()}
	if _, err := d.Read("/etc/phortos/phortosctl.json"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("read: %v", err)
	}
	if _, err := d.List("/etc/apt/auth.conf.d"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("list: %v", err)
	}
}

func TestRemoveTakesAFolderWholeButNeverTheRoot(t *testing.T) {
	d := files.Dir{Root: t.TempDir()}
	_ = d.Write("/usr/local/lib/phortos/phortosctl", []byte("x"), 0o755)
	_ = d.Write("/usr/local/lib/phortos/ca-bundle.pem", []byte("x"), 0o644)
	if err := d.Remove("/usr/local/lib/phortos"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.List("/usr/local/lib/phortos"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("still there: %v", err)
	}
	if err := d.Remove("/usr/local/lib/phortos"); err != nil {
		t.Errorf("missing: %v", err)
	}
	if err := d.Remove("/"); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("root: %v", err)
	}
}
