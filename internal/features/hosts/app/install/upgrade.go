// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// offer is the phortosctl phortos hands out for this platform: phortos' version, the binary's
// name and its hex SHA-256.
type offer struct {
	version, name, sum string
}

// Upgrade replaces the installed phortosctl with the one phortos hands out for this platform (see
// install); without yes it only says what it would do. A host is in step when its binary is byte
// for byte phortos' own.
func (in *Installer) Upgrade(ctx context.Context, yes bool) (string, error) {
	st, err := in.loadState()
	if err != nil {
		return "", err
	}
	if st.Pin == "" {
		return "", fmt.Errorf("this host was configured without a pin, so phortosctl cannot be fetched safely; set it up again with phortos' one-liner: %w", errkind.ErrInvalid)
	}
	o, err := in.offered(ctx, st.API, st.Pin)
	if err != nil {
		return "", err
	}
	if have, err := in.Files.Read(in.Layout.Binary); err == nil && sum(have) == o.sum {
		return "phortosctl is already the one phortos " + o.version + " hands out", nil
	}
	if !yes {
		return "would install the phortosctl phortos " + o.version + " hands out", nil
	}
	if err := in.install(ctx, st, o); err != nil {
		return "", err
	}
	return "installed the phortosctl phortos " + o.version + " hands out", nil
}

// keepUp is what refresh does about a phortosctl that is not the one phortos hands out: install
// it, unless the host was configured not to, and say so ("" when it is in step or nothing can be
// told). A failed upgrade is said, never a failed refresh.
func (in *Installer) keepUp(ctx context.Context, st state.State) string {
	o, outdated := in.outdated(ctx, st.API, st.Pin, in.Layout.Binary)
	if !outdated {
		return ""
	}
	advice := "run `phortosctl upgrade --yes`"
	if st.NoAutoUpgrade {
		return "phortosctl is not the one phortos " + o.version + " hands out; " + advice
	}
	if err := in.install(ctx, st, o); err != nil {
		return "upgrading phortosctl to the one phortos " + o.version + " hands out failed (" + err.Error() + "); " + advice
	}
	return "phortosctl upgraded to the one phortos " + o.version + " hands out, in use from the next update on"
}

// install fetches the phortosctl phortos offers over the pinned API, checks it against the
// checksum phortos publishes, tries it (it must run and say it is phortosctl) and only then puts
// it in place of the installed one, which it keeps as the previous one. Whatever fails before the
// swap leaves the installed phortosctl as it was.
func (in *Installer) install(ctx context.Context, st state.State, o offer) error {
	l := in.Layout
	if in.Programs == nil {
		return fmt.Errorf("nothing to try the new phortosctl with: %w", errkind.ErrInvalid)
	}
	bin, err := in.Phortos.Binary(ctx, st.API, st.Pin, o.name)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", o.name, err)
	}
	if sum(bin) != o.sum {
		return fmt.Errorf("the %s phortos sent does not match its checksum: %w", o.name, errkind.ErrUpstream)
	}
	if err := in.Files.Write(l.Staged, bin, 0o755); err != nil {
		return err
	}
	defer func() {
		if err := in.Files.Remove(l.Staged); err != nil {
			in.Log.Warn("the tried phortosctl not removed", "file", l.Staged, "err", err)
		}
	}()
	out, err := in.Programs.Run(ctx, l.Staged, "version")
	if err != nil {
		return fmt.Errorf("the new phortosctl does not run here: %w", err)
	}
	if !strings.HasPrefix(out, "phortosctl ") {
		first, _, _ := strings.Cut(out, "\n")
		return fmt.Errorf("the new phortosctl answered %q when asked its version: %w", first, errkind.ErrUpstream)
	}
	if old, err := in.Files.Read(l.Binary); err == nil {
		if err := in.Files.Write(l.Previous, old, 0o755); err != nil {
			return err
		}
	}
	return in.Files.Write(l.Binary, bin, 0o755)
}

// offered asks phortos which phortosctl it hands out for this platform; the version is set even
// when phortos carries none for it.
func (in *Installer) offered(ctx context.Context, api, pin string) (offer, error) {
	tools, err := in.Phortos.Tools(ctx, api, pin)
	if err != nil {
		return offer{}, fmt.Errorf("asking phortos which phortosctl it hands out: %w", err)
	}
	o := offer{version: tools.Version, name: "phortosctl-" + in.Platform}
	want, ok := tools.Binaries[o.name]
	if !ok {
		return o, fmt.Errorf("phortos %s carries no %s: %w", tools.Version, o.name, errkind.ErrNotFound)
	}
	o.sum = want
	return o, nil
}

// outdated is what phortos offers, and whether the phortosctl at path is not it. When phortos
// cannot be asked or carries no binary for the platform, nothing can be told: it is not outdated.
func (in *Installer) outdated(ctx context.Context, api, pin, path string) (offer, bool) {
	o, err := in.offered(ctx, api, pin)
	if err != nil {
		in.Log.Debug("phortos' tools not read", "err", err)
		return o, false
	}
	have, err := in.Files.Read(path)
	if err != nil {
		return o, false
	}
	return o, sum(have) != o.sum
}

// sum is a file's hex SHA-256.
func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
