// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package service_test

// Verifies:
//   - The unit runs phortos as its user with its arguments, restarts it always and without limit,
//     and lets it write only to its store and keys folder
//   - Arguments with spaces are quoted as systemd reads them; a folder under /home keeps it visible
//   - phortos install knows its own unit file

import (
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/server/service"
)

func TestTheUnitRunsPhortosSafely(t *testing.T) {
	got := string(service.Render(service.Unit{
		Binary: "/usr/local/bin/phortos", Args: []string{"--dir=/var/lib/phortos/store", "--max-size=100G"},
		User: "phortos", Dirs: []string{"/var/lib/phortos/store", "/var/lib/phortos/keys"},
	}))
	for _, line := range []string{
		"ExecStart=/usr/local/bin/phortos --dir=/var/lib/phortos/store --max-size=100G", "User=phortos", "Restart=always",
		"StartLimitIntervalSec=0", "Type=notify", "WatchdogSec=90", "ExecReload=/bin/kill -HUP $MAINPID", "ProtectSystem=strict", "ProtectHome=yes",
		"ReadWritePaths=/var/lib/phortos/store /var/lib/phortos/keys", "WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("missing %s in\n%s", line, got)
		}
	}
	if !service.Ours([]byte(got)) || service.Ours([]byte("[Unit]\n")) {
		t.Error("ownership misread")
	}
}

func TestArgumentsAreQuotedAndHomeStaysVisible(t *testing.T) {
	got := string(service.Render(service.Unit{
		Binary: "/usr/local/bin/phortos", Args: []string{"--dir=/srv/phortos", "--keys=/home/me/phortos keys"},
		User: "phortos", Dirs: []string{"/srv/phortos", "/home/me/phortos keys"},
	}))
	if !strings.Contains(got, `ExecStart=/usr/local/bin/phortos --dir=/srv/phortos "--keys=/home/me/phortos keys"`) ||
		!strings.Contains(got, `ReadWritePaths=/srv/phortos "/home/me/phortos keys"`) || strings.Contains(got, "ProtectHome") {
		t.Errorf("\n%s", got)
	}
}
