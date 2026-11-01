// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package dist_test

// Verifies:
//   - The binaries' folder is built in, built binaries or not

import (
	"io/fs"
	"testing"

	"github.com/nauarch/phortos/internal/frontdoor/setup/dist"
)

func TestTheBinariesFolderIsBuiltIn(t *testing.T) {
	if _, err := fs.Stat(dist.Files, "bin/README.txt"); err != nil {
		t.Error(err)
	}
}
