// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package install_test

// Verifies:
//   - plan tells phortos' version and whether the phortosctl it would install is phortos' own; on a
//     host configured with --no-auto-upgrade, refresh says so on every update until it is
//   - refresh installs phortos' phortosctl otherwise: tried first, the previous one kept, nothing
//     tried left behind, and only once
//   - an auto-upgrade that fails (a binary that does not match its checksum, one phortos no longer
//     carries, one that does not run or does not say it is phortosctl) is said, fails no refresh,
//     and leaves phortosctl as it was
//   - upgrade needs a host configured with a pin, and a binary phortos carries for the platform
//   - upgrade does nothing to a host already in step, only says what it would do without --yes,
//     refuses a binary that does not match its checksum, and installs a matching one, keeping the
//     previous one
//   - phortos that cannot be asked, or carries no binaries, leaves nothing called outdated

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// hexSum is a hex SHA-256.
func hexSum(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

// offering makes rg's phortos hand out "new binary" for linux-amd64, as v2.
func offering(rg *rig) {
	rg.in.Platform = "linux-amd64"
	rg.phortos.Tooling = ops.Tools{Version: "v2", Binaries: map[string]string{"phortosctl-linux-amd64": hexSum("new binary")}}
	rg.phortos.Binaries = map[string][]byte{"phortosctl-linux-amd64": []byte("new binary")}
}

func TestPlanAndRefreshTellAnOutdatedPhortosctl(t *testing.T) {
	rg := newRig(t)
	offering(rg)
	p, err := rg.in.Plan(t.Context(), rg.target)
	if err != nil || p.PhortosVersion != "v2" || !p.Outdated {
		t.Fatalf("%+v %v", p, err)
	}
	manual := rg.target
	manual.NoAutoUpgrade = true
	if _, err := rg.in.Configure(t.Context(), manual); err != nil {
		t.Fatal(err)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || !strings.Contains(what, "phortosctl upgrade --yes") {
		t.Errorf("%q %v", what, err)
	}
	if _, err := rg.in.Upgrade(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || what != "unchanged" {
		t.Errorf("after upgrade: %q %v", what, err)
	}
}

func TestUpgradeNeedsAPinAndABinary(t *testing.T) {
	rg := newRig(t)
	offering(rg)
	if _, err := rg.in.Configure(t.Context(), hostplan.Target{Server: "phortos.lan:3142"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rg.in.Upgrade(t.Context(), true); errkind.Of(err) != errkind.Invalid {
		t.Errorf("no pin: %v", err)
	}
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	rg.in.Platform = "linux-riscv64"
	if _, err := rg.in.Upgrade(t.Context(), true); errkind.Of(err) != errkind.NotFound {
		t.Errorf("no binary: %v", err)
	}
}

func TestUpgrade(t *testing.T) {
	rg := newRig(t)
	offering(rg)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	if what, err := rg.in.Upgrade(t.Context(), false); err != nil || !strings.HasPrefix(what, "would") {
		t.Errorf("dry run: %q %v", what, err)
	}
	if b, _ := rg.files.Read("/usr/local/lib/phortos/phortosctl"); string(b) != "binary" {
		t.Error("a dry run installed")
	}
	rg.phortos.Binaries["phortosctl-linux-amd64"] = []byte("tampered")
	if _, err := rg.in.Upgrade(t.Context(), true); errkind.Of(err) != errkind.Upstream {
		t.Errorf("tampered: %v", err)
	}
	if b, _ := rg.files.Read("/usr/local/lib/phortos/phortosctl"); string(b) != "binary" {
		t.Error("a tampered binary was installed")
	}
	rg.phortos.Binaries["phortosctl-linux-amd64"] = []byte("new binary")
	if what, err := rg.in.Upgrade(t.Context(), true); err != nil || !strings.HasPrefix(what, "installed") {
		t.Errorf("%q %v", what, err)
	}
	if b, _ := rg.files.Read("/usr/local/lib/phortos/phortosctl"); string(b) != "new binary" || rg.files.Mode("/usr/local/lib/phortos/phortosctl") != 0o755 {
		t.Error("not installed")
	}
	if b, _ := rg.files.Read("/usr/local/lib/phortos/phortosctl.old"); string(b) != "binary" {
		t.Errorf("the previous phortosctl was not kept: %q", b)
	}
	if what, err := rg.in.Upgrade(t.Context(), true); err != nil || !strings.Contains(what, "already") {
		t.Errorf("in step: %q %v", what, err)
	}
}

func TestRefreshUpgradesPhortosctl(t *testing.T) {
	rg := newRig(t)
	offering(rg)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || !strings.Contains(what, "phortosctl upgraded to the one phortos v2 hands out") {
		t.Fatalf("%q %v", what, err)
	}
	for p, want := range map[string]string{
		"/usr/local/lib/phortos/phortosctl":     "new binary",
		"/usr/local/lib/phortos/phortosctl.old": "binary",
	} {
		if b, _ := rg.files.Read(p); string(b) != want {
			t.Errorf("%s: %q", p, b)
		}
	}
	if _, err := rg.files.Read("/usr/local/lib/phortos/phortosctl.new"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("the tried binary was left: %v", err)
	}
	if strings.Join(rg.programs.Ran, ";") != "/usr/local/lib/phortos/phortosctl.new version" {
		t.Errorf("tried as %q", rg.programs.Ran)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || what != "unchanged" {
		t.Errorf("in step: %q %v", what, err)
	}
}

func TestAFailedAutoUpgradeKeepsPhortosctl(t *testing.T) {
	for name, spoil := range map[string]func(*rig){
		"tampered": func(rg *rig) { rg.phortos.Binaries["phortosctl-linux-amd64"] = []byte("tampered") },
		"gone":     func(rg *rig) { delete(rg.phortos.Binaries, "phortosctl-linux-amd64") },
		"does not run": func(rg *rig) {
			rg.programs.Answer = func(string, []string) (string, error) { return "exec format error", errkind.ErrUpstream }
		},
		"not phortosctl": func(rg *rig) {
			rg.programs.Answer = func(string, []string) (string, error) { return "something else 1.0\n", nil }
		},
	} {
		rg := newRig(t)
		offering(rg)
		if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
			t.Fatal(err)
		}
		spoil(rg)
		what, err := rg.in.Refresh(t.Context())
		if err != nil || !strings.Contains(what, "failed") || !strings.Contains(what, "phortosctl upgrade --yes") {
			t.Errorf("%s: %q %v", name, what, err)
		}
		if b, _ := rg.files.Read("/usr/local/lib/phortos/phortosctl"); string(b) != "binary" {
			t.Errorf("%s: phortosctl replaced by %q", name, b)
		}
		for _, p := range []string{"/usr/local/lib/phortos/phortosctl.new", "/usr/local/lib/phortos/phortosctl.old"} {
			if _, err := rg.files.Read(p); errkind.Of(err) != errkind.NotFound {
				t.Errorf("%s: %s left: %v", name, p, err)
			}
		}
	}
}

func TestNothingIsOutdatedWhenPhortosCannotTell(t *testing.T) {
	rg := newRig(t)
	rg.in.Platform = "linux-amd64"
	p, err := rg.in.Plan(t.Context(), rg.target)
	if err != nil || p.Outdated {
		t.Errorf("no binaries: %+v %v", p, err)
	}
}
