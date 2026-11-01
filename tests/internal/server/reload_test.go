// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server_test

// Verifies, end to end:
//   - Settings read again as phortos runs (on a signal, or as it polls) change the hosts served
//     and the hosts intercepted, with a new working CA in the bundle, without a restart
//   - Settings that fail to load, or that cannot run (intercepting without the key folder phortos
//     started with), change nothing

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
)

// settings are what a test's Reload hands phortos, changeable as it runs.
type settings struct {
	mu   sync.Mutex
	o    server.Options
	fail error
}

func (s *settings) set(o server.Options, fail error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.o, s.fail = o, fail
}

func (s *settings) load() (server.Options, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.o, s.fail
}

// eventually waits up to 5s for ok.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("%s: never", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSettingsReloadAsPhortosRuns(t *testing.T) {
	secure := aptrepo.New(aptrepo.Options{Flat: true, TLS: true})
	defer secure.Close()
	plain := aptrepo.New(aptrepo.Options{Flat: true})
	defer plain.Close()
	base := server.Options{Dir: t.TempDir(), Keys: filepath.Join(t.TempDir(), "keys"), Hosts: []string{"deb.debian.org"}, Transport: secure.Transport()}
	s := &settings{o: base}
	now := make(chan struct{})
	o := base
	o.Reload, o.ReloadNow, o.ReloadEvery = s.load, now, 20*time.Millisecond
	p := runWith(t, o)
	defer p.stop()
	if b := pinned(t, p.api); len(b.Hosts) != 0 || len(b.CAs) != 0 {
		t.Fatalf("intercepting before any was asked: %+v", b)
	}

	grown := base
	grown.Intercept = []string{aptrepo.TLSHost}
	s.set(grown, nil)
	now <- struct{}{}
	eventually(t, "the new intercepted host in the bundle", func() bool { return slices.Equal(pinned(t, p.api).Hosts, []string{aptrepo.TLSHost}) })
	b := pinned(t, p.api)
	if len(b.CAs) != 1 {
		t.Fatalf("no working CA for the new host: %+v", b)
	}
	resp, err := throughTunnel(t, p.proxy, b).Get(secure.URL() + secure.InRelease())
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("through the new interception: %v %v", resp, err)
	}
	_ = resp.Body.Close()

	proxyURL, _ := url.Parse("http://" + p.proxy)
	tp := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer tp.CloseIdleConnections()
	ask := func() int {
		resp, err := (&http.Client{Transport: tp}).Get(plain.URL() + plain.InRelease())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := ask(); code != http.StatusForbidden {
		t.Fatalf("a host not served yet: %d", code)
	}
	served := grown
	served.Hosts = append(slices.Clone(base.Hosts), plain.Host())
	s.set(served, nil)
	// The test's upstream reaches only the HTTPS repository, so a host served answers a failed
	// fetch, not 403: what matters is that phortos no longer refuses it.
	eventually(t, "the new host served, as phortos polls", func() bool { return ask() != http.StatusForbidden })

	s.set(served, errors.New("broken file"))
	now <- struct{}{}
	noKeys := served
	noKeys.Keys = ""
	s.set(noKeys, nil)
	now <- struct{}{}
	time.Sleep(100 * time.Millisecond)
	if b2 := pinned(t, p.api); !slices.Equal(b2.Hosts, []string{aptrepo.TLSHost}) || ask() == http.StatusForbidden {
		t.Errorf("failed settings changed what runs: %+v", b2)
	}
}
