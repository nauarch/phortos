// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package service_test

// Verifies:
//   - On a fresh systemd host install copies phortos in, makes its user, a marked store and a keys
//     folder it owns, writes the unit, enables and starts it, and waits for /healthz; nothing
//     happens before Do
//   - Installed again with the same arguments, only the restart and the wait are left; a keys
//     folder holding an identity is used as it is, so configured hosts keep their pin
//   - Without keys the unit writes to the store only
//   - The config file is written when missing and then kept as edited: settings given again for
//     a file saying otherwise are refused, none given leave it be
//   - A folder holding files without the marker (or the identity, for keys), a unit not its own,
//     a relative folder or a host without systemd is refused
//   - Uninstall stops and removes the service and binary, keeping the store and the keys

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server/service"
)

// host is a fresh systemd host under a temporary root, and the commands run on it.
type host struct {
	in  service.Installer
	ran *[]string
}

func newHost(t *testing.T) host {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(t.TempDir(), "phortos")
	if err := os.WriteFile(self, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var ran []string
	users := map[string]bool{}
	return host{ran: &ran, in: service.Installer{
		Root: root, Self: self,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := strings.Join(append([]string{name}, args...), " ")
			ran = append(ran, line)
			switch {
			case name == "id" && !users[args[1]]:
				return nil, errkind.ErrNotFound
			case name == "useradd":
				users[args[len(args)-1]] = true
			}
			return nil, nil
		},
		Healthy: func(context.Context) error { return nil },
	}}
}

// do takes the steps.
func do(t *testing.T, steps []service.Step) {
	t.Helper()
	for _, s := range steps {
		if err := s.Do(context.Background()); err != nil {
			t.Fatalf("%s: %v", s.Say, err)
		}
	}
}

// says lists what the steps say, first lines only.
func says(steps []service.Step) []string {
	var out []string
	for _, s := range steps {
		first, _, _ := strings.Cut(s.Say, "\n")
		out = append(out, first)
	}
	return out
}

const (
	dir  = "/var/lib/phortos/store"
	keys = "/var/lib/phortos/keys"
)

var args = []string{"--dir=" + dir, "--keys=" + keys, "--max-size=100G"}

// at is a host path under the root.
func (h host) at(p string) string { return filepath.Join(h.in.Root, filepath.FromSlash(p)) }

// install works out an install without a config file.
func (h host) install(dir, keys string, args []string) ([]service.Step, error) {
	return h.in.Install(context.Background(), service.Spec{Dir: dir, Keys: keys, Args: args})
}

func TestTheConfigFileIsWrittenOnceThenKeptAsEdited(t *testing.T) {
	const path = "/etc/phortos/phortos.yaml"
	h := newHost(t)
	spec := service.Spec{Dir: dir, Keys: keys, Args: []string{"--config=" + path, "--dir=" + dir}, Config: service.Config{Path: path, Body: []byte("max-size: 100G\n"), Given: true}}
	steps, err := h.in.Install(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	do(t, steps)
	if b, _ := os.ReadFile(h.at(path)); string(b) != "max-size: 100G\n" {
		t.Errorf("written %q", b)
	}
	if again, err := h.in.Install(context.Background(), spec); err != nil || !slices.Contains(says(again), "use "+path+" as it is") {
		t.Errorf("the same settings again: %q %v", says(again), err)
	}
	_ = os.WriteFile(h.at(path), []byte("max-size: 200G\n"), 0o644)
	if _, err := h.in.Install(context.Background(), spec); errkind.Of(err) != errkind.Conflict {
		t.Errorf("settings given for an edited file: %v", err)
	}
	spec.Config.Given = false
	steps, err = h.in.Install(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	do(t, steps)
	if b, _ := os.ReadFile(h.at(path)); string(b) != "max-size: 200G\n" {
		t.Errorf("the edited file was not kept: %q", b)
	}
	spec.Config.Path = "etc/phortos.yaml"
	if _, err := h.in.Install(context.Background(), spec); errkind.Of(err) != errkind.Invalid {
		t.Errorf("relative config: %v", err)
	}
}

func TestAFreshHostGetsPhortosInstalled(t *testing.T) {
	h := newHost(t)
	steps, err := h.install(dir, keys, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(*h.ran) != 1 || (*h.ran)[0] != "id -u phortos" {
		t.Errorf("planning ran %v", *h.ran)
	}
	do(t, steps)
	if data, _ := os.ReadFile(h.at("/usr/local/bin/phortos")); string(data) != "binary" {
		t.Errorf("binary %q", data)
	}
	if _, err := os.Stat(h.at(dir + "/.phortos-volume")); err != nil {
		t.Errorf("store not marked: %v", err)
	}
	if fi, err := os.Stat(h.at(keys)); err != nil || !fi.IsDir() {
		t.Errorf("keys folder not made: %v", err)
	}
	unit, _ := os.ReadFile(h.at("/etc/systemd/system/phortos.service"))
	if !strings.Contains(string(unit), "ExecStart=/usr/local/bin/phortos "+strings.Join(args, " ")+"\n") ||
		!strings.Contains(string(unit), "ReadWritePaths="+dir+" "+keys+"\n") {
		t.Errorf("unit\n%s", unit)
	}
	for _, want := range []string{"useradd --system", "chown -R phortos:phortos " + dir, "chown -R phortos:phortos " + keys, "systemctl daemon-reload", "systemctl enable phortos", "systemctl restart phortos"} {
		if !slices.ContainsFunc(*h.ran, func(r string) bool { return strings.HasPrefix(r, want) }) {
			t.Errorf("never ran %s: %v", want, *h.ran)
		}
	}
	_ = os.WriteFile(h.at(keys+"/identity.pem"), []byte("key"), 0o600)
	again, err := h.install(dir, keys, args)
	if err != nil {
		t.Fatal(err)
	}
	if got := says(again); !slices.Equal(got, []string{
		"copy " + h.in.Self + " to /usr/local/bin/phortos", "use " + dir + ", phortos' store already, owned by phortos",
		"use " + keys + ", phortos' keys folder already, owned by phortos", "enable phortos and (re)start it", "wait until phortos answers /healthz",
	}) {
		t.Errorf("again: %q", got)
	}
}

func TestWithoutKeysOnlyTheStoreIsWritable(t *testing.T) {
	h := newHost(t)
	steps, err := h.install(dir, "", []string{"--dir=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	do(t, steps)
	unit, _ := os.ReadFile(h.at("/etc/systemd/system/phortos.service"))
	if !strings.Contains(string(unit), "ReadWritePaths="+dir+"\n") {
		t.Errorf("unit\n%s", unit)
	}
}

func TestWhatInstallRefuses(t *testing.T) {
	h := newHost(t)
	busy := h.at("/srv/data")
	_ = os.MkdirAll(busy, 0o755)
	_ = os.WriteFile(filepath.Join(busy, "photo.jpg"), []byte("x"), 0o644)
	if _, err := h.install("/srv/data", keys, args); errkind.Of(err) != errkind.Conflict || !strings.Contains(err.Error(), "not phortos' store") {
		t.Errorf("busy store: %v", err)
	}
	if _, err := h.install(dir, "/srv/data", args); errkind.Of(err) != errkind.Conflict || !strings.Contains(err.Error(), "not phortos' keys folder") {
		t.Errorf("busy keys folder: %v", err)
	}
	unit := h.at("/etc/systemd/system/phortos.service")
	_ = os.MkdirAll(filepath.Dir(unit), 0o755)
	_ = os.WriteFile(unit, []byte("[Unit]\n"), 0o644)
	if _, err := h.install(dir, keys, args); errkind.Of(err) != errkind.Conflict {
		t.Errorf("foreign unit: %v", err)
	}
	if _, err := h.in.Uninstall(context.Background()); errkind.Of(err) != errkind.Conflict {
		t.Errorf("uninstalling a foreign unit: %v", err)
	}
	for _, bad := range [][2]string{{"var/lib/phortos", keys}, {dir, "keys"}, {"", keys}} {
		if _, err := h.install(bad[0], bad[1], args); errkind.Of(err) != errkind.Invalid {
			t.Errorf("relative %q %q: %v", bad[0], bad[1], err)
		}
	}
	_ = os.RemoveAll(h.at("/run"))
	if _, err := h.install(dir, keys, args); errkind.Of(err) != errkind.Invalid {
		t.Errorf("no systemd: %v", err)
	}
}

func TestUninstallKeepsTheStoreAndKeys(t *testing.T) {
	h := newHost(t)
	steps, _ := h.install(dir, keys, args)
	do(t, steps)
	steps, err := h.in.Uninstall(context.Background())
	if err != nil || len(steps) != 2 {
		t.Fatalf("%v %v", says(steps), err)
	}
	do(t, steps)
	for _, gone := range []string{"/etc/systemd/system/phortos.service", "/usr/local/bin/phortos"} {
		if _, err := os.Stat(h.at(gone)); !os.IsNotExist(err) {
			t.Errorf("%s left: %v", gone, err)
		}
	}
	for _, kept := range []string{dir + "/.phortos-volume", keys} {
		if _, err := os.Stat(h.at(kept)); err != nil {
			t.Errorf("%s removed: %v", kept, err)
		}
	}
	if !slices.Contains(*h.ran, "systemctl disable --now phortos") {
		t.Errorf("%v", *h.ran)
	}
	if again, err := h.in.Uninstall(context.Background()); err != nil || again != nil {
		t.Errorf("nothing left to uninstall: %v %v", says(again), err)
	}
}
