// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/nauarch/phortos/internal/contracts/ops"
)

// route answers how a client should reach the repository URL in ?url=, as an ops.Route: through
// phortos (at the address the client asked this on) when phortos serves that host for that
// scheme, else DIRECT. An https:// URL goes through phortos only for a host it intercepts on
// port 443, and the answer names the working CA phortos signs with, which the client must have
// installed. Anything phortos cannot read is DIRECT too, so a client never loses its updates to
// a question phortos could not answer.
func (d *door) route(w http.ResponseWriter, r *http.Request) {
	d.reply(w, http.StatusOK, d.decide(r.URL.Query().Get("url"), "http://"+r.Host))
}

// decide is the route for raw, with proxy as phortos' address.
func (d *door) decide(raw, proxy string) ops.Route {
	direct := ops.Route{Proxy: ops.Direct}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return direct
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	switch u.Scheme {
	case "http":
		if d.o.Fetcher.Serves(strings.TrimSuffix(strings.ToLower(u.Host), ":80")) {
			return ops.Route{Proxy: proxy}
		}
	case "https":
		if (port != "" && port != "443") || d.o.Trust == nil || !d.o.Trust.Intercepts(host) || !d.o.Fetcher.Serves(host) {
			return direct
		}
		cas := d.o.Trust.Bundle().CAs
		if len(cas) == 0 {
			return direct
		}
		return ops.Route{Proxy: proxy, CAs: []string{cas[0].Fingerprint}}
	}
	return direct
}
