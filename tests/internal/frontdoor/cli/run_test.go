// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package cli_test

// Verifies:
//   - configure without --yes only plans; with it, it configures; the API defaults to the
//     server's host on port 3143 when a pin is given, and stays empty without one
//   - --server is required; unknown commands and flags exit 2
//   - detect always answers and exits 0, DIRECT for a wrong argument count
//   - refresh is silent when nothing changed, says what changed otherwise, and fails with
//     "keeping what is installed"
//   - unconfigure lists without --yes, removes with it; verify prints apt's warnings
//   - upgrade is a dry run without --yes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/frontdoor/cli"
)

// manager records what it was asked and answers what it is told.
type manager struct {
	calls   []string
	target  hostplan.Target
	refresh string
	err     error
	report  hostplan.Report
}

func (m *manager) Plan(_ context.Context, t hostplan.Target) (hostplan.Plan, error) {
	m.calls, m.target = append(m.calls, "plan"), t
	return hostplan.Plan{Target: t, Writes: []string{"/etc/apt/apt.conf.d/00phortos"}}, m.err
}

func (m *manager) Configure(_ context.Context, t hostplan.Target) (hostplan.Plan, error) {
	m.calls, m.target = append(m.calls, "configure"), t
	return hostplan.Plan{Target: t}, m.err
}

func (m *manager) Unconfigure(_ context.Context, yes bool) ([]string, error) {
	if yes {
		m.calls = append(m.calls, "unconfigure --yes")
	} else {
		m.calls = append(m.calls, "unconfigure")
	}
	return []string{"/etc/apt/apt.conf.d/00phortos"}, m.err
}

func (m *manager) Refresh(context.Context) (string, error) {
	m.calls = append(m.calls, "refresh")
	return m.refresh, m.err
}

func (m *manager) Upgrade(_ context.Context, yes bool) (string, error) {
	m.calls = append(m.calls, fmt.Sprintf("upgrade %v", yes))
	return "installed", m.err
}

func (m *manager) Detect(_ context.Context, url string) string {
	m.calls = append(m.calls, "detect "+url)
	return "http://phortos.lan:3142"
}

func (m *manager) Verify(context.Context) (hostplan.Report, error) {
	m.calls = append(m.calls, "verify")
	return m.report, m.err
}

// run runs a command line against m.
func run(m *manager, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := cli.Run(context.Background(), args, cli.Options{Manager: m, Version: "v1", Out: &out, Err: &errOut})
	return code, out.String(), errOut.String()
}

func TestConfigureIsADryRunWithoutYes(t *testing.T) {
	m := &manager{}
	code, out, _ := run(m, "configure", "--server", "phortos.lan:3142", "--pin", "sha256//x")
	if code != 0 || strings.Join(m.calls, ",") != "plan" || !strings.Contains(out, "Dry run") || m.target.API != "phortos.lan:3143" {
		t.Errorf("%d %v %+v %s", code, m.calls, m.target, out)
	}
	m = &manager{}
	if code, _, _ := run(m, "configure", "--server", "phortos.lan:3142", "--yes"); code != 0 || strings.Join(m.calls, ",") != "configure" || m.target.API != "" {
		t.Errorf("%d %v %+v", code, m.calls, m.target)
	}
}

func TestMisuseExitsTwo(t *testing.T) {
	for _, args := range [][]string{{"configure"}, {"plan", "--nope"}, {"frobnicate"}, {}} {
		if code, _, _ := run(&manager{}, args...); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
}

func TestDetectAlwaysAnswers(t *testing.T) {
	if code, out, _ := run(&manager{}, "detect", "http://deb.debian.org/"); code != 0 || out != "http://phortos.lan:3142\n" {
		t.Errorf("%d %q", code, out)
	}
	for _, args := range [][]string{{"detect"}, {"detect", "a", "b"}} {
		if code, out, _ := run(&manager{}, args...); code != 0 || out != "DIRECT\n" {
			t.Errorf("%v: %d %q", args, code, out)
		}
	}
}

func TestRefreshSpeaksOnlyOfChanges(t *testing.T) {
	if code, out, _ := run(&manager{refresh: "unchanged"}, "refresh"); code != 0 || out != "" {
		t.Errorf("%d %q", code, out)
	}
	if code, out, _ := run(&manager{refresh: "working CAs ab"}, "refresh"); code != 0 || out != "phortosctl: working CAs ab\n" {
		t.Errorf("%d %q", code, out)
	}
	if code, _, errOut := run(&manager{err: errors.New("no route")}, "refresh"); code != 1 || !strings.Contains(errOut, "keeping what is installed") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestUpgradeIsADryRunWithoutYes(t *testing.T) {
	m := &manager{}
	run(m, "upgrade")
	if code, out, _ := run(m, "upgrade", "--yes"); code != 0 || out != "installed\n" || strings.Join(m.calls, ",") != "upgrade false,upgrade true" {
		t.Errorf("%d %q %v", code, out, m.calls)
	}
}

func TestUnconfigureAndVerify(t *testing.T) {
	m := &manager{}
	if code, out, _ := run(m, "unconfigure"); code != 0 || !strings.Contains(out, "Would remove") {
		t.Errorf("%d %q", code, out)
	}
	if code, out, _ := run(m, "unconfigure", "--yes"); code != 0 || !strings.Contains(out, "Removed") || m.calls[1] != "unconfigure --yes" {
		t.Errorf("%d %q %v", code, out, m.calls)
	}
	m = &manager{report: hostplan.Report{Requests: 3, Warnings: []string{"W: Failed to fetch x"}}, err: errors.New("apt warned")}
	if code, out, _ := run(m, "verify"); code != 1 || !strings.Contains(out, "W: Failed to fetch x") || !strings.Contains(out, "3 requests") {
		t.Errorf("%d %q", code, out)
	}
}
