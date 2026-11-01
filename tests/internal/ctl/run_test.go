// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package ctl_test

// Verifies:
//   - phortosctl composes against the real host adapters and answers help without touching apt
//   - A command line it cannot run exits 2; version names the build
//   - detect answers DIRECT on a host it never configured, exiting 0, as apt needs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/ctl"
)

func TestPhortosctlComposesAndAnswers(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := ctl.Run(context.Background(), "v1.2.3", []string{"help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "phortosctl") {
		t.Errorf("%d %s %s", code, out.String(), errOut.String())
	}
	if code := ctl.Run(context.Background(), "v1.2.3", []string{"configure"}, &out, &errOut); code != 2 {
		t.Errorf("configure without --server: %d", code)
	}
	out.Reset()
	if code := ctl.Run(context.Background(), "v1.2.3", []string{"version"}, &out, &errOut); code != 0 || out.String() != "phortosctl v1.2.3 (https://github.com/nauarch/phortos, Apache-2.0)\n" {
		t.Errorf("version: %d %q", code, out.String())
	}
}

func TestDetectOnAnUnconfiguredHostIsDirect(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := ctl.Run(context.Background(), "v1.2.3", []string{"detect", "http://deb.debian.org/debian/"}, &out, &errOut); code != 0 || out.String() != "DIRECT\n" {
		t.Errorf("%d %q %q", code, out.String(), errOut.String())
	}
}
