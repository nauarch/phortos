// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package settings_test

// Verifies:
//   - Flags set phortos' options, lists split on commas and add up, and sizes parse; the minimum
//     rate defaults to the cache's, and 0 turns it off
//   - A refused command line is a usage error; a config file is read only when one is named
//   - The config file's settings apply, the command line's win over them flag by flag, and a
//     setting the file gets wrong (or a file that cannot be read) is an error naming the file
//   - install keeps where the config, store and keys are on the service's command line, puts the
//     rest in the config file with a list's values one by one, and refuses a bare word
//   - A new config file lists the default hosts one by one unless hosts were named, so a host is
//     added to the file without dropping them; read back, it serves exactly them

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/contracts/config"
	"github.com/nauarch/phortos/internal/features/cache"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/internal/server/settings"
)

func TestANewConfigFileListsTheDefaultHosts(t *testing.T) {
	got := settings.WithDefaults([]string{"--max-size=100G", "--intercept=x.example"})
	if !slices.Equal(got[:2], []string{"--max-size=100G", "--intercept=x.example"}) || len(got) != 2+len(server.DefaultHosts) {
		t.Fatalf("%v", got)
	}
	var asked []string
	c, err := settings.Load([]string{"--config=/c.yaml"}, opening(file{args: append(got, "--hosts=mirror.example")}, &asked), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := append(slices.Clone(server.DefaultHosts), "mirror.example"); !slices.Equal(c.Options.Hosts, want) {
		t.Errorf("a host added to the file: serves %v, want %v", c.Options.Hosts, want)
	}
	named := []string{"--hosts=only.example"}
	if got := settings.WithDefaults(named); !slices.Equal(got, named) {
		t.Errorf("hosts named, yet the defaults were added: %v", got)
	}
}

// file is a config file holding args, or failing with err.
type file struct {
	args []string
	err  error
}

func (f file) Args() ([]string, error) { return f.args, f.err }

// opening opens f at any path, recording the paths asked for.
func opening(f file, asked *[]string) func(string) config.File {
	return func(path string) config.File {
		*asked = append(*asked, path)
		return f
	}
}

func TestFlagsSetTheOptions(t *testing.T) {
	c, err := settings.Parse([]string{"--dir=/s", "--hosts=a.example, b.example", "--hosts=c.example", "--max-size=1.5G", "--advertise=phortos.lan", "--min-rate=64K", "--debug"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o := c.Options
	if o.Dir != "/s" || !slices.Equal(o.Hosts, []string{"a.example", "b.example", "c.example"}) || o.MaxBytes != 1610612736 || o.Advertise != "phortos.lan" || o.MinRate != 64<<10 || !c.Debug || o.Addr != ":3142" {
		t.Errorf("%+v debug=%v", o, c.Debug)
	}
	if c, err := settings.Parse([]string{"--dir=/s"}, io.Discard); err != nil || c.Options.MinRate != cache.DefaultMinRate {
		t.Errorf("default min-rate %d %v", c.Options.MinRate, err)
	}
	if c, err := settings.Parse([]string{"--dir=/s", "--min-rate=0"}, io.Discard); err != nil || c.Options.MinRate != 0 {
		t.Errorf("min-rate 0: %d %v", c.Options.MinRate, err)
	}
}

func TestARefusedCommandLineIsAUsageError(t *testing.T) {
	var asked []string
	for _, args := range [][]string{{"--no-such-flag"}, {"--max-size=lots"}} {
		if _, err := settings.Load(args, opening(file{}, &asked), io.Discard); !errors.Is(err, settings.ErrUsage) {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, err := settings.Load([]string{"--dir=/s"}, opening(file{}, &asked), io.Discard); err != nil || len(asked) != 0 {
		t.Errorf("no config named, yet %v asked: %v", asked, err)
	}
}

func TestTheCommandLineWinsOverTheConfigFile(t *testing.T) {
	var asked []string
	f := file{args: []string{"--dir=/from-file", "--max-size=100G", "--intercept=x.example", "--intercept=y.example"}}
	c, err := settings.Load([]string{"--config=/etc/phortos/phortos.yaml", "--dir=/from-flags"}, opening(f, &asked), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	o := c.Options
	if o.Dir != "/from-flags" || o.MaxBytes != 100<<30 || !slices.Equal(o.Intercept, []string{"x.example", "y.example"}) || c.Config != "/etc/phortos/phortos.yaml" {
		t.Errorf("%+v", o)
	}
	if !slices.Equal(asked, []string{"/etc/phortos/phortos.yaml"}) {
		t.Errorf("asked %v", asked)
	}
}

func TestAConfigFileGotWrongIsAnErrorNamingIt(t *testing.T) {
	var asked []string
	_, err := settings.Load([]string{"--config=/c.yaml"}, opening(file{args: []string{"--max-size=lots"}}, &asked), io.Discard)
	if err == nil || errors.Is(err, settings.ErrUsage) || !strings.Contains(err.Error(), "config file /c.yaml") {
		t.Errorf("a bad setting: %v", err)
	}
	_, err = settings.Load([]string{"--config=/c.yaml"}, opening(file{err: errkind.ErrInvalid}, &asked), io.Discard)
	if errkind.Of(err) != errkind.Invalid {
		t.Errorf("an unreadable file: %v", err)
	}
}

func TestInstallSplitsTheSettings(t *testing.T) {
	unit, inFile, err := settings.Split([]string{"--config=/c.yaml", "--dir=/s", "--keys=/k", "--max-size=100G", "--intercept=x.example, y.example", "--debug"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(unit, []string{"--config=/c.yaml", "--dir=/s", "--keys=/k"}) {
		t.Errorf("unit %v", unit)
	}
	if !slices.Equal(inFile, []string{"--max-size=100G", "--intercept=x.example", "--intercept=y.example", "--debug"}) {
		t.Errorf("file %v", inFile)
	}
	if _, _, err := settings.Split([]string{"--dir=/s", "stray"}); errkind.Of(err) != errkind.Invalid {
		t.Errorf("a bare word: %v", err)
	}
}
