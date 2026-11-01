// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package client_test

// Verifies, against servers in the test:
//   - The bundle, the tools and the binaries come over TLS only when the server's key matches the
//     pin, the bundle with that key given back; another key is refused before the request is sent
//   - The route answer and the request counters are read from the proxy; a status other than 200
//     is an upstream failure, no server at all is unreachable

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/phortos/client"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/features/trust/domain/certs"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// api serves a bundle over TLS with a fresh identity, counting requests; it gives the address
// and the identity's pin.
func api(t *testing.T, asked *atomic.Int64) (string, string) {
	t.Helper()
	key, _ := certs.NewKey()
	id, err := certs.IdentityCertificate(key, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	pin := certs.Pin(&key.PublicKey)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/trust":
			_ = json.NewEncoder(w).Encode(trust.Bundle{Pin: pin, Hosts: []string{"download.docker.com"}})
		case "/get/tools":
			_ = json.NewEncoder(w).Encode(ops.Tools{Version: "v2", Binaries: map[string]string{"phortosctl-linux-amd64": "ab"}})
		case "/get/phortosctl-linux-amd64":
			_, _ = w.Write([]byte("binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{id}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String(), pin
}

func TestTheBundleComesOnlyFromThePinnedKey(t *testing.T) {
	var asked atomic.Int64
	addr, pin := api(t, &asked)
	c := client.New(5 * time.Second)
	b, key, err := c.Trust(t.Context(), addr, pin)
	if err != nil || b.Pin != pin || key == nil || certs.Pin(key) != pin {
		t.Fatalf("%+v %v", b, err)
	}
	other, _ := certs.NewKey()
	_, _, err = c.Trust(t.Context(), addr, certs.Pin(&other.PublicKey))
	if errkind.Of(err) != errkind.Forbidden || !strings.Contains(err.Error(), "pinned") {
		t.Errorf("another pin: %v", err)
	}
	if asked.Load() != 1 {
		t.Errorf("the server was asked %d times; a wrong key must stop the request", asked.Load())
	}
}

func TestToolsAndBinariesComeOnlyFromThePinnedKey(t *testing.T) {
	var asked atomic.Int64
	addr, pin := api(t, &asked)
	c := client.New(5 * time.Second)
	if tools, err := c.Tools(t.Context(), addr, pin); err != nil || tools.Version != "v2" || tools.Binaries["phortosctl-linux-amd64"] != "ab" {
		t.Errorf("%+v %v", tools, err)
	}
	if b, err := c.Binary(t.Context(), addr, pin, "phortosctl-linux-amd64"); err != nil || string(b) != "binary" {
		t.Errorf("%q %v", b, err)
	}
	other, _ := certs.NewKey()
	if _, err := c.Binary(t.Context(), addr, certs.Pin(&other.PublicKey), "phortosctl-linux-amd64"); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("another pin: %v", err)
	}
	if asked.Load() != 2 {
		t.Errorf("asked %d times", asked.Load())
	}
}

func TestRouteAndCountersAreRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/route":
			_ = json.NewEncoder(w).Encode(ops.Route{Proxy: "http://" + r.Host, CAs: []string{r.URL.Query().Get("url")}})
		case "/metrics":
			_, _ = w.Write([]byte("# HELP x\nphortos_file_requests_total{result=\"hit\"} 7\nphortos_file_requests_total{result=\"miss\"} 2\n" +
				"phortos_release_requests_total{result=\"fresh\"} 3\nphortos_served_bytes_total 999\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	server := strings.TrimPrefix(srv.URL, "http://")
	c := client.New(5 * time.Second)
	r, err := c.Route(t.Context(), server, "https://download.docker.com/a b")
	if err != nil || r.Proxy != srv.URL || r.CAs[0] != "https://download.docker.com/a b" {
		t.Errorf("%+v %v", r, err)
	}
	if n, err := c.Requests(t.Context(), server); err != nil || n != 12 {
		t.Errorf("%d %v", n, err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	gone := ln.Addr().String()
	_ = ln.Close()
	if _, err := c.Route(t.Context(), gone, "http://x/"); errkind.Of(err) != errkind.Unreachable {
		t.Errorf("no server: %v", err)
	}
}

func TestAnUnexpectedStatusIsAnUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := client.New(0).Requests(t.Context(), strings.TrimPrefix(srv.URL, "http://")); errkind.Of(err) != errkind.Upstream {
		t.Errorf("%v", err)
	}
}
