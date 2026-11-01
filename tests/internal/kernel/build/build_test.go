// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package build_test

// Verifies:
//   - A stamped version is taken as it is
//   - Without one, the recorded commit names the build, marked when it had changes not committed
//   - About names the binary, its version, where it is published and its licence

import (
	"runtime/debug"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/build"
)

func TestAStampedVersionIsTakenAsItIs(t *testing.T) {
	if got := build.Version("v1.2.3"); got != "v1.2.3" {
		t.Error(got)
	}
	if got := build.Version("dev"); got == "" {
		t.Error("an unstamped build has no version")
	}
}

func TestTheCommitNamesADevBuild(t *testing.T) {
	for _, c := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "1a2b3c4d5e6f"}}, "dev-1a2b3c4d"},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "1a2b3c4d5e6f"}, {Key: "vcs.modified", Value: "true"}}, "dev-1a2b3c4d-dirty"},
	} {
		if got := build.Dev(c.settings); got != c.want {
			t.Errorf("%v: %q, want %q", c.settings, got, c.want)
		}
	}
}

func TestAboutNamesTheHomeAndTheLicence(t *testing.T) {
	if got := build.About("phortos", "v1.2.3"); got != "phortos v1.2.3 (https://github.com/nauarch/phortos, Apache-2.0)" {
		t.Error(got)
	}
}
