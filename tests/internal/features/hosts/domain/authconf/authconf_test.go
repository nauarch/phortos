// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package authconf_test

// Verifies:
//   - Hosts come from machine entries in any layout (one line or several), lower case, sorted,
//     once; a scheme, port or path is dropped, an IPv6 address unbracketed
//   - Comments, logins and passwords are skipped, so a password named "machine" is not read

import (
	"slices"
	"testing"

	"github.com/nauarch/phortos/internal/features/hosts/domain/authconf"
)

func TestHostsComeFromMachineEntries(t *testing.T) {
	data := `# Proxmox subscription
machine enterprise.proxmox.com/debian/pve login KEY password SECRET
machine https://Repo.Example.com:8443/debian
  login u
  password machine
machine [2001:db8::1]:443 login a password b
# machine commented.example.com login x password y
machine enterprise.proxmox.com login k password s
`
	got := authconf.Hosts([]byte(data))
	want := []string{"2001:db8::1", "enterprise.proxmox.com", "repo.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("%q", got)
	}
	if got := authconf.Hosts(nil); len(got) != 0 {
		t.Errorf("empty: %q", got)
	}
}
