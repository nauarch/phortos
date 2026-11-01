// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package keys_test

// Verifies:
//   - A key reads back as saved, from a folder made on demand
//   - A missing key is not found; names with separators or a leading dot are invalid
//   - On Unix the folder and the files are their owner's alone, and a file others could read is
//     refused

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/storage/keys"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAKeyReadsBackAsSaved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "keys")
	d, err := keys.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Save("identity.pem", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Load("identity.pem"); err != nil || string(got) != "secret" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := d.Load("ca.pem"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("missing: %v", err)
	}
	for _, n := range []string{"", "../x", `a\b`, ".hidden"} {
		if err := d.Save(n, nil); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%q: %v", n, err)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	if info, _ := os.Stat(root); info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Join(root, "identity.pem")); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", info.Mode().Perm())
	}
	_ = os.Chmod(filepath.Join(root, "identity.pem"), 0o644)
	if _, err := d.Load("identity.pem"); errkind.Of(err) != errkind.Invalid {
		t.Errorf("a key others can read: %v", err)
	}
}
