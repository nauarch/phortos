// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package install puts phortosctl's files on a host and keeps them current: the binary, the
// detect program, the CA bundle, the kept state and the apt configuration, written in that order
// so apt never sees a configuration whose files are missing, and removed in the reverse order.
package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"slices"
	"strings"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/features/hosts/domain/apparmor"
	"github.com/nauarch/phortos/internal/features/hosts/domain/aptconf"
	"github.com/nauarch/phortos/internal/features/hosts/domain/authconf"
	"github.com/nauarch/phortos/internal/features/hosts/domain/bundle"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Installer installs from Self, the path of the running phortosctl, built for Platform
// (linux-amd64), the platform whose binary phortos hands out for this host. Programs tries a
// new phortosctl before an upgrade installs it.
type Installer struct {
	Files    host.Files
	Apt      host.Apt
	Programs host.Programs
	AppArmor host.AppArmor
	Phortos  host.Phortos
	Layout   layout.Layout
	Self     string
	Platform string
	Clock    clock.Clock
	Log      *slog.Logger
}

// prepared is a plan with the bytes it would write.
type prepared struct {
	plan    hostplan.Plan
	state   state.State
	checked bundle.Checked
}

// Plan reads the host and phortos and says what Configure would do.
func (in *Installer) Plan(ctx context.Context, t hostplan.Target) (hostplan.Plan, error) {
	p, err := in.prepare(ctx, t)
	return p.plan, err
}

// Configure writes everything, refusing conflicting proxy settings.
func (in *Installer) Configure(ctx context.Context, t hostplan.Target) (hostplan.Plan, error) {
	p, err := in.prepare(ctx, t)
	if err != nil {
		return p.plan, err
	}
	if len(p.plan.Conflicts) > 0 {
		return p.plan, fmt.Errorf("the host has proxy settings of its own (%s); remove them first: %w",
			strings.Join(p.plan.Conflicts, "; "), errkind.ErrConflict)
	}
	l := in.Layout
	if in.Self != l.Binary {
		self, err := in.Files.Read(in.Self)
		if err != nil {
			return p.plan, fmt.Errorf("reading phortosctl itself: %w", err)
		}
		if err := in.Files.Write(l.Binary, self, 0o755); err != nil {
			return p.plan, err
		}
	}
	if err := in.Files.Write(l.Detect, aptconf.DetectScript(l), 0o755); err != nil {
		return p.plan, err
	}
	if err := in.writeBundle(p.checked); err != nil {
		return p.plan, err
	}
	if err := in.Files.Write(l.State, state.Encode(p.state), 0o644); err != nil {
		return p.plan, err
	}
	if _, err := in.disallowDetect(ctx); err != nil {
		in.Log.Warn("phortosctl's AppArmor block not removed", "file", l.ESMLocal, "err", err)
	}
	return p.plan, in.Files.Write(l.AptConf, aptconf.Render(l, p.state.Hosts), 0o644)
}

// Unconfigure removes what Configure wrote, the apt configuration first, then phortosctl's block
// in the ESM job's AppArmor additions, the state and the installed files (phortosctl's own
// folder), and the state's folder when nothing else is left in it (phortos' own config file may
// be, on a host that runs phortos too); without yes it only lists them.
func (in *Installer) Unconfigure(ctx context.Context, yes bool) ([]string, error) {
	l := in.Layout
	paths := []string{l.AptConf, l.State, l.Lib}
	if !yes {
		return paths, nil
	}
	if err := in.Files.Remove(l.AptConf); err != nil {
		return paths, err
	}
	if _, err := in.disallowDetect(ctx); err != nil {
		in.Log.Warn("phortosctl's AppArmor block not removed", "file", l.ESMLocal, "err", err)
	}
	for _, p := range paths[1:] {
		if err := in.Files.Remove(p); err != nil {
			return paths, err
		}
	}
	if left, err := in.Files.List(path.Dir(l.State)); err == nil && len(left) == 0 {
		if err := in.Files.Remove(path.Dir(l.State)); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

// allowDetect lets Ubuntu Pro's ESM job run detect, on a host that has the job's AppArmor
// profile, and says whether it changed anything (see apparmor). Nothing calls it by default: apt
// reaches the ESM repositories DIRECT (aptconf.Direct), so the job never runs detect. It is kept
// for a command that sends ESM through phortos on hosts that want it; the block as written is
// refused under the job's no_new_privs, so that command needs to change it first.
func (in *Installer) allowDetect(ctx context.Context) (bool, error) {
	return in.esmAdditions(ctx, func(local []byte) []byte { return apparmor.With(local, in.Layout) })
}

// disallowDetect takes phortosctl's block out of the ESM job's AppArmor additions again; Configure
// and Refresh call it so a block an earlier phortosctl wrote does not stay behind.
func (in *Installer) disallowDetect(ctx context.Context) (bool, error) {
	return in.esmAdditions(ctx, apparmor.Without)
}

// esmAdditions changes the ESM job's local AppArmor additions and reloads its profile, on a host
// that has the profile. A reload that fails puts the additions back as they were, so a rule
// AppArmor refuses never stays behind to break the profile's next load.
func (in *Installer) esmAdditions(ctx context.Context, change func([]byte) []byte) (bool, error) {
	l := in.Layout
	if _, err := in.Files.Read(l.ESMProfile); err != nil {
		return false, nil
	}
	local, err := in.Files.Read(l.ESMLocal)
	had := err == nil
	if err != nil && errkind.Of(err) != errkind.NotFound {
		return false, err
	}
	want := change(local)
	if bytes.Equal(want, local) {
		return false, nil
	}
	if in.AppArmor == nil {
		return false, fmt.Errorf("no AppArmor to reload %s with: %w", l.ESMProfile, errkind.ErrInvalid)
	}
	if err := in.Files.Write(l.ESMLocal, want, 0o644); err != nil {
		return false, err
	}
	if err := in.AppArmor.Reload(ctx, l.ESMProfile); err != nil {
		undo := in.Files.Remove(l.ESMLocal)
		if had {
			undo = in.Files.Write(l.ESMLocal, local, 0o644)
		}
		return false, errors.Join(fmt.Errorf("AppArmor refused the change to %s, so it was undone: %w", l.ESMLocal, err), undo)
	}
	return true, nil
}

// Refresh renews the bundle, the state and the apt configuration, from what phortos publishes
// now and what this phortosctl writes; it rewrites the apt configuration whenever it differs from
// what it would write, so a host picks up a newer phortosctl's configuration on its own. It
// installs the phortosctl phortos hands out when it differs, unless the host was configured not
// to (see keepUp), so the next refresh is the new phortosctl's. A bundle
// that fails its checks changes nothing. apt runs it before an update whose configuration it
// already loaded, so a newly intercepted host only goes through phortos from the next run on (see
// state.Settle).
func (in *Installer) Refresh(ctx context.Context) (string, error) {
	l := in.Layout
	st, err := in.loadState()
	if err != nil {
		return "", err
	}
	st.Credentialed = in.credentialed()
	var changes []string
	hosts := st.Written()
	if st.Pin != "" {
		checked, err := in.trust(ctx, hostplan.Target{Server: st.Server, API: st.API, Pin: st.Pin})
		if err != nil {
			return "", err
		}
		if !slices.Equal(checked.Hosts, hosts) {
			changes = append(changes, "intercepted hosts "+orNone(checked.Hosts)+" (new ones from the next update on)")
		}
		st.Settle(checked.Hosts)
		hosts = checked.Hosts
		if !slices.Equal(fingerprints(checked.CAs), fingerprints(st.CAs)) {
			changes = append(changes, "working CAs "+strings.Join(fingerprints(checked.CAs), ", "))
		}
		if err := in.writeBundle(checked); err != nil {
			return "", err
		}
		st.CAs = checked.CAs
		if what := in.keepUp(ctx, st); what != "" {
			changes = append(changes, what)
		}
	}
	if rewrote, err := in.writeAptConf(hosts); err != nil {
		return "", err
	} else if rewrote && len(changes) == 0 {
		changes = append(changes, "apt configuration rewritten")
	}
	if changed, err := in.disallowDetect(ctx); err != nil {
		in.Log.Warn("phortosctl's AppArmor block not removed", "file", l.ESMLocal, "err", err)
	} else if changed {
		changes = append(changes, "phortosctl's AppArmor block for Ubuntu Pro's ESM job removed (ESM goes DIRECT)")
	}
	st.Refreshed = in.Clock.Now().UTC()
	if err := in.Files.Write(l.State, state.Encode(st), 0o644); err != nil {
		return "", err
	}
	if len(changes) == 0 {
		return "unchanged", nil
	}
	return strings.Join(changes, "; "), nil
}

// prepare reads everything a plan needs.
func (in *Installer) prepare(ctx context.Context, t hostplan.Target) (prepared, error) {
	l := in.Layout
	p := prepared{plan: hostplan.Plan{Target: t}}
	if err := validate(t); err != nil {
		return p, err
	}
	if _, err := in.Files.Read(l.State); err == nil {
		p.plan.Configured = true
	}
	dump, err := in.Apt.Config(ctx)
	if err != nil {
		return p, fmt.Errorf("reading apt's configuration: %w", err)
	}
	p.plan.Conflicts = aptconf.Conflicts(dump, l)
	p.state = state.State{Server: t.Server, API: t.API, Pin: t.Pin, NoAutoUpgrade: t.NoAutoUpgrade, Credentialed: in.credentialed(), Refreshed: in.Clock.Now().UTC()}
	if t.Pin != "" {
		if p.checked, err = in.trust(ctx, t); err != nil {
			return p, err
		}
		p.state.Hosts, p.state.CAs = p.checked.Hosts, p.checked.CAs
		o, outdated := in.outdated(ctx, t.API, t.Pin, in.Self)
		p.plan.PhortosVersion, p.plan.Outdated = o.version, outdated
	}
	p.plan.Intercepted, p.plan.Credentialed = p.state.Hosts, p.state.Credentialed
	for _, c := range p.state.CAs {
		p.plan.CAs = append(p.plan.CAs, hostplan.CA{Fingerprint: c.Fingerprint, NotAfter: c.NotAfter})
	}
	p.plan.Writes = []string{l.Binary, l.Detect}
	if len(p.state.Hosts) > 0 {
		p.plan.Writes = append(p.plan.Writes, l.Bundle)
	}
	p.plan.Writes = append(p.plan.Writes, l.State)
	p.plan.Writes = append(p.plan.Writes, l.AptConf)
	return p, nil
}

// trust fetches the bundle over the pinned connection and checks it.
func (in *Installer) trust(ctx context.Context, t hostplan.Target) (bundle.Checked, error) {
	b, identity, err := in.Phortos.Trust(ctx, t.API, t.Pin)
	if err != nil {
		return bundle.Checked{}, fmt.Errorf("fetching phortos' trust bundle from %s: %w", t.API, err)
	}
	return bundle.Check(b, identity, t.Pin, in.Clock.Now())
}

// writeAptConf writes the apt configuration for hosts when it differs from the file, and says
// whether it did.
func (in *Installer) writeAptConf(hosts []string) (bool, error) {
	l := in.Layout
	want := aptconf.Render(l, hosts)
	if old, err := in.Files.Read(l.AptConf); err == nil && bytes.Equal(old, want) {
		return false, nil
	}
	return true, in.Files.Write(l.AptConf, want, 0o644)
}

// writeBundle writes the CA file, when there are hosts to check against it and it changed.
func (in *Installer) writeBundle(c bundle.Checked) error {
	l := in.Layout
	if len(c.Hosts) == 0 {
		return nil
	}
	system, err := in.Files.Read(l.SystemCAs)
	if err != nil {
		return fmt.Errorf("reading the host's CAs: %w", err)
	}
	file := bundle.File(system, c)
	if old, err := in.Files.Read(l.Bundle); err == nil && bytes.Equal(old, file) {
		return nil
	}
	return in.Files.Write(l.Bundle, file, 0o644)
}

// loadState reads the kept state; a host never configured is a failure that says so.
func (in *Installer) loadState() (state.State, error) {
	b, err := in.Files.Read(in.Layout.State)
	if errkind.Of(err) == errkind.NotFound {
		return state.State{}, fmt.Errorf("this host is not configured for phortos (run phortosctl configure): %w", errkind.ErrNotFound)
	}
	if err != nil {
		return state.State{}, err
	}
	return state.Decode(b)
}

// credentialed are the hosts in apt's credentials files; unreadable ones are logged and skipped.
func (in *Installer) credentialed() []string {
	l := in.Layout
	files := []string{l.AuthConf}
	if names, err := in.Files.List(l.AuthConfDir); err == nil {
		for _, n := range names {
			if strings.HasSuffix(n, ".conf") {
				files = append(files, path.Join(l.AuthConfDir, n))
			}
		}
	}
	var hosts []string
	for _, f := range files {
		data, err := in.Files.Read(f)
		if err != nil {
			if errkind.Of(err) != errkind.NotFound {
				in.Log.Warn("apt credentials file not read", "file", f, "err", err)
			}
			continue
		}
		hosts = append(hosts, authconf.Hosts(data)...)
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// validate refuses a target that cannot be written into apt's configuration.
func validate(t hostplan.Target) error {
	var problems []error
	if _, _, err := net.SplitHostPort(t.Server); err != nil || strings.ContainsAny(t.Server, " \t\"'/") {
		problems = append(problems, fmt.Errorf("server %q is not host:port", t.Server))
	}
	if t.Pin != "" {
		if !strings.HasPrefix(t.Pin, "sha256//") || len(t.Pin) != len("sha256//")+44 {
			problems = append(problems, fmt.Errorf("pin %q is not sha256//<base64 SHA-256>", t.Pin))
		}
		if _, _, err := net.SplitHostPort(t.API); err != nil {
			problems = append(problems, fmt.Errorf("API %q is not host:port", t.API))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("%w: %w", err, errkind.ErrInvalid)
	}
	return nil
}

// orNone joins names, or says none.
func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// fingerprints lists CAs' fingerprints.
func fingerprints(cas []state.CA) []string {
	out := make([]string, 0, len(cas))
	for _, c := range cas {
		out = append(out, c.Fingerprint)
	}
	return out
}
