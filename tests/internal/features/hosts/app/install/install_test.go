// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package install_test

// Verifies:
//   - A target with a server that is not host:port, or a malformed pin, is invalid
//   - Without a pin phortos' API is never asked: no bundle, no intercepted hosts, plain repositories
//     only
//   - configure writes each file with its mode, and does not copy phortosctl onto itself
//   - unconfigure removes phortosctl's files only, keeping phortos' own config beside its state
//   - Ubuntu Pro's ESM repositories go DIRECT; on a host with the ESM job's profile, configure,
//     refresh and unconfigure take out a block an earlier phortosctl wrote (keeping the admin's
//     own) and reload it, and reload nothing when there is none; a change AppArmor refuses is
//     undone without failing configure; a host without the profile is left alone
//   - Credentials come from auth.conf and auth.conf.d/*.conf only
//   - refresh on a host never configured says so; a failing fetch changes nothing; new intercepted
//     hosts rewrite the apt configuration, and go through phortos only from the next refresh on,
//     since the run whose hook wrote their CaInfo loaded its configuration before; an apt
//     configuration other than what this phortosctl writes is rewritten, with a pin or without
//   - A bundle phortos signed with another key is refused and nothing is written

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/features/hosts/app/install"
	"github.com/nauarch/phortos/internal/features/hosts/domain/apparmor"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
	"github.com/nauarch/phortos/tests/kit/fakehost"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// rig is an installer over fakes, with a published bundle for download.docker.com.
type rig struct {
	in       *install.Installer
	files    *fakehost.Files
	phortos  *fakehost.Phortos
	programs *fakehost.Programs
	target   hostplan.Target
}

func newRig(t *testing.T) *rig {
	t.Helper()
	b, key, pin, err := fakehost.Signed([]string{"download.docker.com"}, now)
	if err != nil {
		t.Fatal(err)
	}
	fs := fakehost.NewFiles()
	_ = fs.Write("/opt/phortosctl", []byte("binary"), 0o755)
	_ = fs.Write("/etc/ssl/certs/ca-certificates.crt", []byte("SYSTEM\n"), 0o644)
	ph := &fakehost.Phortos{Bundle: b, Key: key}
	pr := &fakehost.Programs{}
	return &rig{
		in: &install.Installer{
			Files: fs, Apt: &fakehost.Apt{}, Programs: pr, Phortos: ph, Layout: layout.Linux(), Self: "/opt/phortosctl",
			Clock: kitclock.At(now), Log: slog.New(slog.DiscardHandler),
		},
		files: fs, phortos: ph, programs: pr,
		target: hostplan.Target{Server: "phortos.lan:3142", API: "phortos.lan:3143", Pin: pin},
	}
}

func TestBadTargetsAreInvalid(t *testing.T) {
	rg := newRig(t)
	for _, tg := range []hostplan.Target{
		{Server: "phortos.lan"},
		{Server: "phortos.lan:3142/x"},
		{Server: "phortos.lan:3142", API: "phortos.lan:3143", Pin: "sha256//short"},
		{Server: "phortos.lan:3142", Pin: rg.target.Pin},
	} {
		if _, err := rg.in.Plan(t.Context(), tg); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%+v: %v", tg, err)
		}
	}
}

func TestWithoutAPinTheAPIIsNeverAsked(t *testing.T) {
	rg := newRig(t)
	p, err := rg.in.Configure(t.Context(), hostplan.Target{Server: "phortos.lan:3142"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rg.phortos.Asked) != 0 || len(p.Intercepted) != 0 || strings.Contains(strings.Join(p.Writes, " "), "ca-bundle") {
		t.Errorf("asked %v, plan %+v", rg.phortos.Asked, p)
	}
	conf, _ := rg.files.Read("/etc/apt/apt.conf.d/00phortos")
	if strings.Contains(string(conf), "CaInfo") {
		t.Error("CaInfo without a pin")
	}
}

func TestConfigureWritesEachFileWithItsMode(t *testing.T) {
	rg := newRig(t)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	for p, mode := range map[string]uint32{
		"/usr/local/lib/phortos/phortosctl":    0o755,
		"/usr/local/lib/phortos/detect":        0o755,
		"/usr/local/lib/phortos/ca-bundle.pem": 0o644,
		"/etc/phortos/phortosctl.json":         0o644,
		"/etc/apt/apt.conf.d/00phortos":        0o644,
	} {
		if _, err := rg.files.Read(p); err != nil || rg.files.Mode(p) != mode {
			t.Errorf("%s: %v, mode %o", p, err, rg.files.Mode(p))
		}
	}
	rg.in.Self = "/usr/local/lib/phortos/phortosctl"
	_ = rg.files.Write(rg.in.Self, []byte("installed"), 0o755)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	if b, _ := rg.files.Read(rg.in.Self); string(b) != "installed" {
		t.Error("phortosctl was copied onto itself")
	}
}

func TestUnconfigureLeavesWhatIsNotItsOwn(t *testing.T) {
	rg := newRig(t)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	_ = rg.files.Write("/etc/phortos/phortos.yaml", []byte("max-size: 100G\n"), 0o644)
	if _, err := rg.in.Unconfigure(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := rg.files.Read("/etc/phortos/phortosctl.json"); errkind.Of(err) != errkind.NotFound {
		t.Errorf("state left: %v", err)
	}
	if b, err := rg.files.Read("/etc/phortos/phortos.yaml"); err != nil || string(b) != "max-size: 100G\n" {
		t.Errorf("phortos' own config removed: %v", err)
	}
}

// earlier are the ESM job's local additions as an earlier phortosctl left them: the admin's own
// rule and phortosctl's block.
var earlier = string(apparmor.With([]byte("/srv/admin r,\n"), layout.Linux()))

// withESM puts Ubuntu Pro's ESM profile on the rig's host, with the local additions an earlier
// phortosctl left, and an AppArmor that fails as fail says (nil: never).
func withESM(rg *rig, fail func([]byte) error) *fakehost.AppArmor {
	l := layout.Linux()
	_ = rg.files.Write(l.ESMProfile, []byte("profile ubuntu_pro_esm_cache {}\n"), 0o644)
	_ = rg.files.Write(l.ESMLocal, []byte(earlier), 0o644)
	aa := &fakehost.AppArmor{Files: rg.files, Local: l.ESMLocal, Fail: fail}
	rg.in.AppArmor = aa
	return aa
}

func TestUbuntuProsESMGoesDirect(t *testing.T) {
	rg := newRig(t)
	l := layout.Linux()
	aa := withESM(rg, nil)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	conf, _ := rg.files.Read(l.AptConf)
	if !strings.Contains(string(conf), `Acquire::https::Proxy::esm.ubuntu.com "DIRECT";`) {
		t.Errorf("ESM is not DIRECT:\n%s", conf)
	}
	if local, _ := rg.files.Read(l.ESMLocal); string(local) != "/srv/admin r,\n" || len(aa.Reloaded) != 1 || aa.Reloaded[0] != l.ESMProfile {
		t.Fatalf("configure left %q, reloaded %v", local, aa.Reloaded)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || what != "unchanged" || len(aa.Reloaded) != 1 {
		t.Errorf("nothing to take out, yet: %q %v %v", what, err, aa.Reloaded)
	}
	_ = rg.files.Write(l.ESMLocal, []byte(earlier), 0o644)
	if what, err := rg.in.Refresh(t.Context()); err != nil || !strings.Contains(what, "AppArmor block") {
		t.Errorf("refresh left an earlier block: %q %v", what, err)
	}
	_ = rg.files.Write(l.ESMLocal, []byte(earlier), 0o644)
	if _, err := rg.in.Unconfigure(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if local, _ := rg.files.Read(l.ESMLocal); string(local) != "/srv/admin r,\n" || len(aa.Reloaded) != 3 {
		t.Errorf("after unconfigure: %q, reloaded %v", local, aa.Reloaded)
	}
}

func TestARefusedAppArmorChangeIsUndone(t *testing.T) {
	rg := newRig(t)
	l := layout.Linux()
	withESM(rg, func(local []byte) error {
		if !strings.Contains(string(local), "phortos_detect") {
			return errkind.ErrUpstream
		}
		return nil
	})
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatalf("configure failed over the ESM job: %v", err)
	}
	if local, _ := rg.files.Read(l.ESMLocal); string(local) != earlier {
		t.Errorf("the refused change stayed: %q", local)
	}
	if _, err := rg.files.Read(l.AptConf); err != nil {
		t.Errorf("apt was not configured: %v", err)
	}
}

func TestRefreshRewritesAnOutdatedAptConfiguration(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		rg := newRig(t)
		tg := rg.target
		if !pinned {
			tg = hostplan.Target{Server: tg.Server}
		}
		if _, err := rg.in.Configure(t.Context(), tg); err != nil {
			t.Fatal(err)
		}
		l := layout.Linux()
		want, _ := rg.files.Read(l.AptConf)
		_ = rg.files.Write(l.AptConf, []byte("// an earlier phortosctl's\n"), 0o644)
		if what, err := rg.in.Refresh(t.Context()); err != nil || what != "apt configuration rewritten" {
			t.Errorf("pin %q: %q %v", tg.Pin, what, err)
		}
		if conf, _ := rg.files.Read(l.AptConf); string(conf) != string(want) {
			t.Errorf("pin %q: rewritten as\n%s", tg.Pin, conf)
		}
		if what, err := rg.in.Refresh(t.Context()); err != nil || what != "unchanged" {
			t.Errorf("pin %q: %q %v", tg.Pin, what, err)
		}
	}
}

func TestWithoutTheESMProfileAppArmorIsLeftAlone(t *testing.T) {
	rg := newRig(t)
	aa := &fakehost.AppArmor{Files: rg.files}
	rg.in.AppArmor = aa
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	if _, err := rg.files.Read(layout.Linux().ESMLocal); errkind.Of(err) != errkind.NotFound || len(aa.Reloaded) != 0 {
		t.Errorf("AppArmor touched without the profile: %v %v", err, aa.Reloaded)
	}
}

func TestCredentialsComeFromAptsFilesOnly(t *testing.T) {
	rg := newRig(t)
	_ = rg.files.Write("/etc/apt/auth.conf", []byte("machine a.example.com login x password y\n"), 0o600)
	_ = rg.files.Write("/etc/apt/auth.conf.d/b.conf", []byte("machine b.example.com login x password y\n"), 0o600)
	_ = rg.files.Write("/etc/apt/auth.conf.d/c.conf.bak", []byte("machine c.example.com login x password y\n"), 0o600)
	p, err := rg.in.Plan(t.Context(), rg.target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Credentialed, ",") != "a.example.com,b.example.com" {
		t.Errorf("%v", p.Credentialed)
	}
}

func TestRefresh(t *testing.T) {
	rg := newRig(t)
	if _, err := rg.in.Refresh(t.Context()); errkind.Of(err) != errkind.NotFound {
		t.Errorf("never configured: %v", err)
	}
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	before, _ := rg.files.Read("/etc/phortos/phortosctl.json")
	rg.phortos.TrustErr = errkind.ErrUnreachable
	if _, err := rg.in.Refresh(t.Context()); err == nil {
		t.Error("a failing fetch passed")
	}
	if after, _ := rg.files.Read("/etc/phortos/phortosctl.json"); string(after) != string(before) {
		t.Error("a failing refresh changed the state")
	}
	b, _, _, _ := fakehost.Signed([]string{"developer.download.nvidia.com", "download.docker.com"}, now)
	b.Pin = rg.target.Pin
	rg.phortos.TrustErr, rg.phortos.Bundle = nil, b
	if _, err := rg.in.Refresh(t.Context()); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("a bundle signed by another key: %v", err)
	}
	if after, _ := rg.files.Read("/etc/phortos/phortosctl.json"); string(after) != string(before) {
		t.Error("a refused bundle changed the state")
	}
}

func TestNewInterceptedHostsRewriteTheAptConfiguration(t *testing.T) {
	rg := newRig(t)
	if _, err := rg.in.Configure(t.Context(), rg.target); err != nil {
		t.Fatal(err)
	}
	b, key, pin, _ := fakehost.Signed([]string{"developer.download.nvidia.com", "download.docker.com"}, now)
	rg.phortos.Bundle, rg.phortos.Key = b, key
	kept, _ := rg.files.Read("/etc/phortos/phortosctl.json")
	_ = rg.files.Write("/etc/phortos/phortosctl.json", []byte(strings.ReplaceAll(string(kept), rg.target.Pin, pin)), 0o644)
	what, err := rg.in.Refresh(t.Context())
	if err != nil || !strings.Contains(what, "intercepted hosts") {
		t.Fatalf("%q %v", what, err)
	}
	conf, _ := rg.files.Read("/etc/apt/apt.conf.d/00phortos")
	if !strings.Contains(string(conf), "developer.download.nvidia.com::CaInfo") {
		t.Errorf("%s", conf)
	}
	st := readState(t, rg)
	if st.Intercepts("developer.download.nvidia.com") || !st.Intercepts("download.docker.com") || len(st.Pending) != 1 {
		t.Errorf("the run that wrote the CaInfo must not use it: %+v", st)
	}
	if what, err := rg.in.Refresh(t.Context()); err != nil || what != "unchanged" {
		t.Errorf("%q %v", what, err)
	}
	if st := readState(t, rg); !st.Intercepts("developer.download.nvidia.com") || len(st.Pending) != 0 {
		t.Errorf("the next run must use it: %+v", st)
	}
}

// readState reads the kept state.
func readState(t *testing.T, rg *rig) state.State {
	t.Helper()
	b, err := rg.files.Read("/etc/phortos/phortosctl.json")
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
