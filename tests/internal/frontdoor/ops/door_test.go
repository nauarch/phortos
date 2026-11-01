// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package ops_test

// Verifies:
//   - /healthz answers 200 when phortos can serve and 503 with the reason when it cannot
//   - /metrics gives every counter in Prometheus' text format, the version escaped
//   - /api/route sends a served http:// host through phortos at the address it was asked on, and
//     anything else straight: hosts not served, unreadable URLs, other schemes
//   - An https:// host goes through phortos only when intercepted, on port 443, and the answer
//     names the current working CA; without an authority every https:// URL is DIRECT
//   - Other paths and methods are not served

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/trust"
	opsdoor "github.com/nauarch/phortos/internal/frontdoor/ops"
)

// fake is a scripted reporter and fetcher.
type fake struct {
	health error
	stats  ops.Stats
}

// Healthy answers the scripted health.
func (f *fake) Healthy() error { return f.health }

// Stats answers the scripted counters.
func (f *fake) Stats() ops.Stats { return f.stats }

// Serves serves deb.debian.org, a test repository on a port, and download.docker.com.
func (f *fake) Serves(host string) bool {
	return host == "deb.debian.org" || host == "127.0.0.1:8080" || host == "download.docker.com"
}

// Get is never asked.
func (f *fake) Get(context.Context, string, repo.Ask) (repo.File, error) {
	return repo.File{}, errors.New("not asked")
}

// authority intercepts download.docker.com and signs with two CAs, the current one first.
type authority struct{}

// Intercepts download.docker.com.
func (authority) Intercepts(host string) bool { return host == "download.docker.com" }

// Bundle has a current and a next CA.
func (authority) Bundle() trust.Bundle {
	return trust.Bundle{CAs: []trust.CA{{Fingerprint: "aaaa"}, {Fingerprint: "bbbb"}}}
}

// do sends one request to a door.
func do(f *fake, tr opsdoor.Trust, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, nil)
	r.Host = "phortos.lan:3142"
	opsdoor.New(opsdoor.Options{Reporter: f, Fetcher: f, Trust: tr, Version: `v1.2.3"x`}).ServeHTTP(w, r)
	return w
}

// route asks the door how to reach raw.
func route(t *testing.T, tr opsdoor.Trust, raw string) ops.Route {
	t.Helper()
	w := do(&fake{}, tr, http.MethodGet, "/api/route?url="+url.QueryEscape(raw))
	var got ops.Route
	if w.Code != http.StatusOK || json.NewDecoder(w.Body).Decode(&got) != nil {
		t.Fatalf("%s: %d %q", raw, w.Code, w.Body)
	}
	return got
}

func TestHealthzTellsWhetherPhortosCanServe(t *testing.T) {
	if w := do(&fake{}, nil, http.MethodGet, "/healthz"); w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Errorf("healthy: %d %q", w.Code, w.Body)
	}
	w := do(&fake{health: errors.New("marker unreadable")}, nil, http.MethodGet, "/healthz")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "marker unreadable") {
		t.Errorf("unhealthy: %d %q", w.Code, w.Body)
	}
}

func TestMetricsGiveEveryCounter(t *testing.T) {
	w := do(&fake{stats: ops.Stats{Hits: 7, ReleasesStale: 2, StoreBytes: 1 << 20, MaxBytes: 1 << 30}}, nil, http.MethodGet, "/metrics")
	body := w.Body.String()
	for _, want := range []string{
		`phortos_build_info{version="v1.2.3\"x"} 1`,
		`phortos_file_requests_total{result="hit"} 7`,
		`phortos_release_requests_total{result="stale"} 2`,
		"phortos_store_bytes 1048576",
		"phortos_store_max_bytes 1073741824",
		"# TYPE phortos_evicted_files_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestPlainHostsAreRoutedByTheHostList(t *testing.T) {
	for raw, want := range map[string]string{
		"http://deb.debian.org/debian/dists/trixie/InRelease":    "http://phortos.lan:3142",
		"http://DEB.debian.org:80/debian/":                       "http://phortos.lan:3142",
		"http://127.0.0.1:8080/repo/":                            "http://phortos.lan:3142",
		"http://download.example.com/x":                          ops.Direct,
		"http://user:pw@deb.debian.org/debian/":                  ops.Direct,
		"ftp://deb.debian.org/debian/":                           ops.Direct,
		"not a url":                                              ops.Direct,
		"":                                                       ops.Direct,
		"https://deb.debian.org/debian/dists/trixie/InRelease":   ops.Direct,
		"https://download.docker.com/linux/debian/dists/trixie/": ops.Direct,
	} {
		if got := route(t, nil, raw); got.Proxy != want || len(got.CAs) != 0 {
			t.Errorf("%q: %+v, want %s", raw, got, want)
		}
	}
}

func TestInterceptedHostsAreRoutedWithTheirCA(t *testing.T) {
	got := route(t, authority{}, "https://download.docker.com/linux/debian/dists/trixie/InRelease")
	if got.Proxy != "http://phortos.lan:3142" || len(got.CAs) != 1 || got.CAs[0] != "aaaa" {
		t.Errorf("intercepted: %+v", got)
	}
	for _, raw := range []string{"https://download.docker.com:8443/x", "https://deb.debian.org/debian/"} {
		if got := route(t, authority{}, raw); got.Proxy != ops.Direct {
			t.Errorf("%s: %+v", raw, got)
		}
	}
}

func TestOtherPathsAreNotServed(t *testing.T) {
	for _, c := range []struct{ method, path string }{{http.MethodGet, "/"}, {http.MethodPost, "/healthz"}, {http.MethodPost, "/api/route"}} {
		if w := do(&fake{}, nil, c.method, c.path); w.Code < 400 {
			t.Errorf("%s %s: %d", c.method, c.path, w.Code)
		}
	}
}
