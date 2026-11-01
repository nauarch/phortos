// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package verify_test

// Verifies:
//   - An update that reached phortos without warnings passes, counting the requests
//   - apt's warnings fail it even though apt exited 0, as apt does for a failed TLS check
//   - An update that never reached phortos fails, and so does one apt failed
//   - A host never configured is told so

import (
	"errors"
	"testing"

	"github.com/nauarch/phortos/internal/features/hosts/app/verify"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/tests/kit/fakehost"
)

// verifier is a verifier over a configured fake host whose update reaches phortos n times.
func verifier(out string, uerr error, n int64) *verify.Verifier {
	fs := fakehost.NewFiles()
	_ = fs.Write(layout.Linux().State, state.Encode(state.State{Server: "phortos.lan:3142"}), 0o644)
	ph := &fakehost.Phortos{}
	return &verify.Verifier{Files: fs, Apt: &fakehost.Apt{Out: out, Err: uerr, During: func() { ph.Add(n) }}, Phortos: ph, Layout: layout.Linux()}
}

func TestAnUpdateThroughPhortosPasses(t *testing.T) {
	r, err := verifier("Hit:1 http://deb.debian.org/debian trixie InRelease\nReading package lists...\n", nil, 4).Verify(t.Context())
	if err != nil || r.Requests != 4 || len(r.Warnings) != 0 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestWarningsFailIt(t *testing.T) {
	out := "Hit:1 ...\nW: Failed to fetch https://download.docker.com/linux/debian/dists/trixie/InRelease  Certificate verification failed\nW: Some index files failed to download.\n"
	r, err := verifier(out, nil, 3).Verify(t.Context())
	if err == nil || len(r.Warnings) != 2 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestMissingPhortosOrAFailedUpdateFails(t *testing.T) {
	if _, err := verifier("Hit:1 ...\n", nil, 0).Verify(t.Context()); err == nil {
		t.Error("an update that never reached phortos passed")
	}
	if _, err := verifier("E: oops\n", errors.New("exit status 100"), 2).Verify(t.Context()); err == nil {
		t.Error("a failed update passed")
	}
}

func TestAHostNeverConfiguredIsToldSo(t *testing.T) {
	v := &verify.Verifier{Files: fakehost.NewFiles(), Apt: &fakehost.Apt{}, Phortos: &fakehost.Phortos{}, Layout: layout.Linux()}
	if _, err := v.Verify(t.Context()); err == nil {
		t.Error("passed")
	}
}
