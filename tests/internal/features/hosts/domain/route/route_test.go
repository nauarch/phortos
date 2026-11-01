// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package route_test

// Verifies, for each case apt can meet:
//   - Plain http:// goes through phortos when phortos says so, always to the configured server
//     (never to an address in the answer)
//   - https:// goes through phortos only for an intercepted host whose CA is installed and valid
//   - Anything else is DIRECT: phortos not asked, phortos says DIRECT, credentials, an unreadable
//     URL, a CA missing, expired or not yet valid, a host with no CaInfo, a server apt cannot take

import (
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/features/hosts/domain/route"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
)

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

const proxy = "http://phortos.lan:3142"

func TestEveryCaseAptMeets(t *testing.T) {
	st := state.State{
		Server:       "phortos.lan:3142",
		Hosts:        []string{"download.docker.com"},
		CAs:          []state.CA{{Fingerprint: "aa", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}},
		Credentialed: []string{"enterprise.proxmox.com"},
	}
	through := ops.Route{Proxy: "http://elsewhere:9"}
	withCA := ops.Route{Proxy: "http://elsewhere:9", CAs: []string{"aa"}}
	for _, c := range []struct {
		name  string
		url   string
		r     ops.Route
		asked bool
		st    state.State
		at    time.Time
		want  string
	}{
		{"plain", "http://deb.debian.org/debian/dists/trixie/InRelease", through, true, st, now, proxy},
		{"plain, not asked", "http://deb.debian.org/debian/", through, false, st, now, ops.Direct},
		{"plain, phortos says direct", "http://deb.debian.org/debian/", ops.Route{Proxy: ops.Direct}, true, st, now, ops.Direct},
		{"credentials", "http://Enterprise.Proxmox.com/debian/pve", through, true, st, now, ops.Direct},
		{"unreadable", "::", through, true, st, now, ops.Direct},
		{"intercepted", "https://download.docker.com/linux/debian/", withCA, true, st, now, proxy},
		{"intercepted, no CA named", "https://download.docker.com/linux/debian/", through, true, st, now, ops.Direct},
		{"intercepted, other CA", "https://download.docker.com/x", ops.Route{Proxy: proxy, CAs: []string{"bb"}}, true, st, now, ops.Direct},
		{"intercepted, CA expired", "https://download.docker.com/x", withCA, true, st, now.Add(2 * time.Hour), ops.Direct},
		{"intercepted, clock behind", "https://download.docker.com/x", withCA, true, st, now.Add(-2 * time.Hour), ops.Direct},
		{"https, no CaInfo", "https://developer.download.nvidia.com/x", withCA, true, st, now, ops.Direct},
		{"server apt cannot take", "http://deb.debian.org/", through, true, state.State{Server: "a b:1"}, now, ops.Direct},
		{"ftp", "ftp://deb.debian.org/", through, true, st, now, ops.Direct},
	} {
		if got := route.Decide(c.url, c.r, c.asked, c.st, c.at); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
