// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package layout_test

// Verifies:
//   - Every Linux path is absolute, and what phortosctl installs sits under its own folder

import (
	"path"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
)

func TestLinuxPathsAreAbsoluteAndOurOwn(t *testing.T) {
	l := layout.Linux()
	for _, p := range []string{l.Lib, l.Binary, l.Detect, l.Bundle, l.State, l.AptConf, l.SystemCAs, l.AuthConf, l.AuthConfDir} {
		if !path.IsAbs(p) || path.Clean(p) != p {
			t.Errorf("%q", p)
		}
	}
	for _, p := range []string{l.Binary, l.Detect, l.Bundle} {
		if !strings.HasPrefix(p, l.Lib+"/") {
			t.Errorf("%s is not under %s", p, l.Lib)
		}
	}
}
