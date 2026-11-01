// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package route decides what detect tells apt for one URL: phortos' proxy address or DIRECT. It
// errs towards DIRECT, so a broken trust chain or an unreachable phortos costs caching, never an
// update.
package route

import (
	"net/url"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
)

// Decide is the answer for raw, given what phortos answered (asked is false when it could not be
// asked), the kept state and the time. It goes through phortos only when phortos says so, the
// host has no credentials, and, for https://, the host is one whose certificates apt checks
// against the bundle and the CA phortos signs with is installed and valid now. The proxy is the
// configured server, never an address from the answer, which comes over plain HTTP.
func Decide(raw string, r ops.Route, asked bool, st state.State, now time.Time) string {
	proxy := "http://" + st.Server
	if !asked || r.Proxy == ops.Direct || !plainProxy(proxy) {
		return ops.Direct
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ops.Direct
	}
	host := strings.ToLower(u.Hostname())
	if st.HasCredentials(host) {
		return ops.Direct
	}
	switch u.Scheme {
	case "http":
		return proxy
	case "https":
		if len(r.CAs) > 0 && st.Intercepts(host) && st.Valid(r.CAs[0], now) {
			return proxy
		}
	}
	return ops.Direct
}

// plainProxy reports whether p is an http:// address with a host and nothing else, which apt
// takes as it is.
func plainProxy(p string) bool {
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "http" && u.Host != "" && u.Path == "" && !strings.ContainsAny(p, " \t\r\n\"'")
}
