// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Where phortos install puts things.
const (
	// Binary is where phortos is installed.
	Binary = "/usr/local/bin/phortos"
	// UnitPath is phortos' systemd unit.
	UnitPath = "/etc/systemd/system/phortos.service"
	// User is the system user phortos runs as.
	User = "phortos"
	// marker says a folder is phortos' store, and identity a folder is its keys folder (the
	// store's and the authority's own names, repeated to keep this package free of both).
	marker   = ".phortos-volume"
	identity = "identity.pem"
)

// Runner runs a command and gives what it printed.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Installer installs phortos as a systemd service on this host, or removes it. Root is where the
// host's filesystem is seen from ("/" on a host, a folder in tests); Self is the running binary,
// copied into place; Healthy asks the installed phortos whether it answers.
type Installer struct {
	Root    string
	Self    string
	Run     Runner
	Healthy func(ctx context.Context) error
}

// Step is one thing an install or uninstall does: what it says, and the doing.
type Step struct {
	Say string
	Do  func(ctx context.Context) error
}

// Spec is what install sets up: the store's folder, the keys folder ("" for none), the arguments
// phortos serves with (--dir, --keys and --config among them), and its config file.
type Spec struct {
	Dir    string
	Keys   string
	Args   []string
	Config Config
}

// Config is phortos' config file: where it is ("" for none) and what install writes when it is
// missing. A file already there is kept as it is, since its settings may have been edited; Given
// says settings were given to install for it, which it then refuses, since they belong in the
// file.
type Config struct {
	Path  string
	Body  []byte
	Given bool
}

// Install works out installing phortos as s says. A folder holding files but no marker (no
// identity, for keys) is refused: it is not phortos'.
func (in Installer) Install(ctx context.Context, s Spec) ([]Step, error) {
	dir, keys := s.Dir, s.Keys
	for _, f := range [][2]string{{"--dir", dir}, {"--keys", keys}, {"--config", s.Config.Path}} {
		if f[1] != "" && !path.IsAbs(f[1]) || f[0] == "--dir" && f[1] == "" {
			return nil, fmt.Errorf("%s %q: want an absolute path: %w", f[0], f[1], errkind.ErrInvalid)
		}
	}
	if _, err := os.Stat(in.at("/run/systemd/system")); err != nil {
		return nil, fmt.Errorf("this host does not run systemd; run phortos in a container instead: %w", errkind.ErrInvalid)
	}
	var steps []Step
	if same, _ := sameFile(in.Self, in.at(Binary)); !same {
		steps = append(steps, Step{"copy " + in.Self + " to " + Binary, func(context.Context) error { return in.copySelf() }})
	}
	if _, err := in.Run(ctx, "id", "-u", User); err != nil {
		steps = append(steps, Step{"create the system user " + User, func(ctx context.Context) error {
			_, err := in.Run(ctx, "useradd", "--system", "--no-create-home", "--home-dir", dir, "--shell", "/usr/sbin/nologin", User)
			return err
		}})
	}
	dirs := []string{dir}
	store, err := in.folder(dir, marker, "store", "--dir", 0o750, true)
	if err != nil {
		return nil, err
	}
	steps = append(steps, store...)
	if keys != "" {
		kept, err := in.folder(keys, identity, "keys folder", "--keys", 0o700, false)
		if err != nil {
			return nil, err
		}
		steps = append(steps, kept...)
		dirs = append(dirs, keys)
	}
	conf, err := in.config(s.Config)
	if err != nil {
		return nil, err
	}
	steps = append(steps, conf...)
	unit := Render(Unit{Binary: Binary, Args: s.Args, User: User, Dirs: dirs})
	current, err := os.ReadFile(in.at(UnitPath))
	switch {
	case err == nil && !Ours(current):
		return nil, fmt.Errorf("%s exists and was not written by phortos install; remove it first: %w", UnitPath, errkind.ErrConflict)
	case !bytes.Equal(current, unit):
		steps = append(steps, Step{"write " + UnitPath + ":\n" + indent(unit), func(context.Context) error {
			return write(in.at(UnitPath), unit, 0o644)
		}})
	}
	steps = append(steps,
		Step{"enable phortos and (re)start it", func(ctx context.Context) error {
			for _, args := range [][]string{{"daemon-reload"}, {"enable", "phortos"}, {"restart", "phortos"}} {
				if _, err := in.Run(ctx, "systemctl", args...); err != nil {
					return err
				}
			}
			return nil
		}},
		Step{"wait until phortos answers /healthz", in.await},
	)
	return steps, nil
}

// Uninstall works out removing the service and the binary. The store, the keys and the user
// stay: the store is a cache anyone can delete, the keys are what configured hosts are pinned
// to, and a user may own files elsewhere.
func (in Installer) Uninstall(ctx context.Context) ([]Step, error) {
	current, err := os.ReadFile(in.at(UnitPath))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case !Ours(current):
		return nil, fmt.Errorf("%s was not written by phortos install; leaving it: %w", UnitPath, errkind.ErrConflict)
	}
	return []Step{
		{"stop and disable phortos", func(ctx context.Context) error {
			_, err := in.Run(ctx, "systemctl", "disable", "--now", "phortos")
			return err
		}},
		{"remove " + UnitPath + " and " + Binary, func(ctx context.Context) error {
			for _, p := range []string{UnitPath, Binary} {
				if err := os.Remove(in.at(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
			}
			_, err := in.Run(ctx, "systemctl", "daemon-reload")
			return err
		}},
	}, nil
}

// folder works out one of phortos' folders, named what and given as flag: used as it is when it
// holds sign, made with perm (and marked with sign when mark) when missing or empty, refused when
// it holds anything else.
func (in Installer) folder(dir, sign, what, flag string, perm fs.FileMode, mark bool) ([]Step, error) {
	full := in.at(dir)
	if _, err := os.Stat(filepath.Join(full, sign)); err == nil {
		return []Step{{"use " + dir + ", phortos' " + what + " already, owned by " + User, func(ctx context.Context) error {
			_, err := in.Run(ctx, "chown", "-R", User+":"+User, dir)
			return err
		}}}, nil
	}
	entries, err := os.ReadDir(full)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, fmt.Errorf("%s holds files but no %s, so it is not phortos' %s; pick another %s: %w", dir, sign, what, flag, errkind.ErrConflict)
	}
	return []Step{{"make " + dir + " phortos' " + what + ", owned by " + User, func(ctx context.Context) error {
		if err := os.MkdirAll(full, perm); err != nil {
			return err
		}
		if err := os.Chmod(full, perm); err != nil {
			return err
		}
		if mark {
			if err := write(filepath.Join(full, sign), nil, 0o640); err != nil {
				return err
			}
		}
		_, err := in.Run(ctx, "chown", "-R", User+":"+User, dir)
		return err
	}}}, nil
}

// config works out the config file: written when missing, kept when there, refused when
// settings were given for one that is there and says otherwise.
func (in Installer) config(c Config) ([]Step, error) {
	if c.Path == "" {
		return nil, nil
	}
	current, err := os.ReadFile(in.at(c.Path))
	switch {
	case err == nil && c.Given && !bytes.Equal(current, c.Body):
		return nil, fmt.Errorf("%s exists: change settings there (phortos applies them as it runs), not on install's command line: %w", c.Path, errkind.ErrConflict)
	case err == nil:
		return []Step{{"use " + c.Path + " as it is", func(context.Context) error { return nil }}}, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	return []Step{{"write " + c.Path + ":\n" + indent(c.Body), func(context.Context) error {
		return write(in.at(c.Path), c.Body, 0o644)
	}}}, nil
}

// await waits up to a minute for the installed phortos to answer.
func (in Installer) await(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		err := in.Healthy(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("phortos did not answer within a minute (journalctl -u phortos says why): %w", err)
		case <-time.After(time.Second):
		}
	}
}

// copySelf copies the running binary into place through a temporary file and a rename, which also
// replaces a binary that is running.
func (in Installer) copySelf() error {
	data, err := os.ReadFile(in.Self)
	if err != nil {
		return err
	}
	return write(in.at(Binary), data, 0o755)
}

// at is where a host path lives under the root.
func (in Installer) at(p string) string { return filepath.Join(in.Root, filepath.FromSlash(p)) }

// write replaces a file whole through a temporary file beside it.
func write(p string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// sameFile reports whether two paths are the one file.
func sameFile(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ia, ib), nil
}

// indent indents a file for printing under its step.
func indent(data []byte) string {
	return "    " + string(bytes.ReplaceAll(bytes.TrimRight(data, "\n"), []byte("\n"), []byte("\n    ")))
}
