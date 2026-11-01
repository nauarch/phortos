// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server_test

// Verifies, end to end as apt and phortosctl would see it:
//   - With hosts to intercept, the API serves the trust bundle over TLS with the identity, whose
//     key matches the pin the bundle carries
//   - An HTTPS repository is served through CONNECT with a certificate from the bundle's CA, and
//     its files are stored: asked again, upstream is not
//   - /api/route sends that repository through phortos and names the bundle's current CA
//   - The key folder is kept across a restart, so the pin stays
//   - A key folder without hosts to intercept still serves the API: the bundle (pin, no hosts, no
//     CAs), the setup script and the tools
//   - The one-liner phortos logs names the advertised name, or the machine's without one
//   - Hosts to intercept without a key folder refuse to start

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nauarch/phortos/internal/contracts/ops"
	contract "github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
)

// running is a phortos started for a test, with its proxy and API addresses.
type running struct {
	proxy, api string
	stop       func()
}

// runIntercepting starts phortos intercepting the repository's host.
func runIntercepting(t *testing.T, r *aptrepo.Repo, dir, keys string) running {
	t.Helper()
	return runWith(t, server.Options{Dir: dir, Hosts: []string{"deb.debian.org"}, Intercept: []string{r.Host()}, Keys: keys, Transport: r.Transport()})
}

// runWith starts phortos with o, on free loopback ports, and waits until it listens.
func runWith(t *testing.T, o server.Options) running {
	t.Helper()
	return runLogging(t, o, slog.New(slog.DiscardHandler))
}

// runLogging is runWith logging to log.
func runLogging(t *testing.T, o server.Options, log *slog.Logger) running {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	addr, apiAddr := make(chan string, 1), make(chan string, 1)
	done := make(chan error, 1)
	o.Addr, o.APIAddr, o.Init, o.Host, o.PID = "127.0.0.1:0", "127.0.0.1:0", true, "test", 1
	o.Listening, o.APIListening = func(a string) { addr <- a }, func(a string) { apiAddr <- a }
	go func() { done <- server.Run(ctx, o, log) }()
	var out running
	for out.proxy == "" {
		select {
		case out.api = <-apiAddr:
		case out.proxy = <-addr:
		case err := <-done:
			t.Fatal(err)
		}
	}
	out.stop = func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stopping: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("phortos did not stop")
		}
	}
	return out
}

// pinned fetches the bundle as phortosctl does: the API's certificate is trusted only for the
// key it presents, which must match the pin the bundle then carries.
func pinned(t *testing.T, api string) contract.Bundle {
	t.Helper()
	var seen string
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			cert, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
			seen = "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
			return nil
		},
	}}}
	resp, err := c.Get("https://" + api + "/trust")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b contract.Bundle
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.Pin == "" || b.Pin != seen {
		t.Fatalf("the API's key %s is not the bundle's pin %s", seen, b.Pin)
	}
	return b
}

// throughTunnel is a client using phortos as its proxy and trusting the bundle's CAs only.
func throughTunnel(t *testing.T, proxyAddr string, b contract.Bundle) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, ca := range b.CAs {
		block, _ := pem.Decode([]byte(ca.PEM))
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(cert)
	}
	p, _ := url.Parse("http://" + proxyAddr)
	tp := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(tp.CloseIdleConnections)
	return &http.Client{Transport: tp}
}

func TestAnHTTPSRepositoryIsInterceptedAndStored(t *testing.T) {
	r := aptrepo.New(aptrepo.Options{Flat: true, TLS: true})
	defer r.Close()
	dir, keys := t.TempDir(), filepath.Join(t.TempDir(), "keys")
	p := runIntercepting(t, r, dir, keys)
	b := pinned(t, p.api)
	if len(b.Hosts) != 1 || b.Hosts[0] != aptrepo.TLSHost || len(b.CAs) != 1 {
		t.Fatalf("%+v", b)
	}
	c := throughTunnel(t, p.proxy, b)
	paths := []string{r.InRelease(), r.Index(".gz"), r.Deb(0)}
	for round := range 2 {
		for _, path := range paths {
			resp, err := c.Get(r.URL() + path)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 || !bytes.Equal(body, r.File(path)) {
				t.Errorf("round %d, %s: %d, %d bytes", round, path, resp.StatusCode, len(body))
			}
		}
	}
	if n := r.Hits(r.Deb(0)); n != 1 {
		t.Errorf("the package reached upstream %d times", n)
	}
	resp, err := http.Get("http://" + p.proxy + "/api/route?url=" + url.QueryEscape(r.URL()+r.InRelease()))
	if err != nil {
		t.Fatal(err)
	}
	var rt ops.Route
	_ = json.NewDecoder(resp.Body).Decode(&rt)
	_ = resp.Body.Close()
	if rt.Proxy != "http://"+p.proxy || len(rt.CAs) != 1 || rt.CAs[0] != b.CAs[0].Fingerprint {
		t.Errorf("route: %+v", rt)
	}
	p.stop()
	again := runIntercepting(t, r, dir, keys)
	defer again.stop()
	if b2 := pinned(t, again.api); b2.Pin != b.Pin || b2.CAs[0].PEM != b.CAs[0].PEM {
		t.Error("the identity or the CA changed across a restart")
	}
}

func TestKeysWithoutInterceptionServeTheAPIAndSetup(t *testing.T) {
	tools := fstest.MapFS{"phortosctl-linux-amd64": {Data: []byte("binary")}}
	p := runWith(t, server.Options{Dir: t.TempDir(), Keys: filepath.Join(t.TempDir(), "keys"), Tools: tools, Version: "v9"})
	defer p.stop()
	b := pinned(t, p.api)
	if len(b.Hosts) != 0 || len(b.CAs) != 0 {
		t.Errorf("%+v", b)
	}
	c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := c.Get("https://" + p.api + "/setup")
	if err != nil {
		t.Fatal(err)
	}
	script, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Contains(script, []byte("pin='"+b.Pin+"'")) {
		t.Errorf("setup: %d %.200s", resp.StatusCode, script)
	}
	resp, err = c.Get("https://" + p.api + "/get/tools")
	if err != nil {
		t.Fatal(err)
	}
	var tl ops.Tools
	_ = json.NewDecoder(resp.Body).Decode(&tl)
	_ = resp.Body.Close()
	if tl.Version != "v9" || len(tl.Binaries) != 1 {
		t.Errorf("tools: %+v", tl)
	}
}

// lockedBuffer is a buffer safe to log to from several goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestTheLoggedOneLinerNamesTheAdvertisedName(t *testing.T) {
	for advertise, want := range map[string]string{"": "https://test:", "phortos.lan": "https://phortos.lan:"} {
		var out lockedBuffer
		p := runLogging(t, server.Options{Dir: t.TempDir(), Keys: filepath.Join(t.TempDir(), "keys"), Advertise: advertise}, slog.New(slog.NewTextHandler(&out, nil)))
		p.stop()
		if !strings.Contains(out.String(), want+portOf(t, p.api)+"/setup") {
			t.Errorf("advertise %q:\n%s", advertise, out.String())
		}
	}
}

// portOf is an address's port.
func portOf(t *testing.T, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestInterceptingWithoutKeysRefusesToStart(t *testing.T) {
	err := server.Run(t.Context(), server.Options{Dir: t.TempDir(), Intercept: []string{"download.docker.com"}}, slog.New(slog.DiscardHandler))
	if errkind.Of(err) != errkind.Invalid {
		t.Errorf("got %v", err)
	}
}
