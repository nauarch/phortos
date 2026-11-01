// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package proxy_test

// Verifies, with the real trust feature and a real HTTPS client using the door as its proxy:
//   - An intercepted host is served through CONNECT as an https:// URL, with a certificate that
//     verifies against the bundle's CA, over several requests on one tunnel
//   - A host not intercepted, a port other than 443, or no issuer at all is refused with 403/400
//   - A TLS server name other than the CONNECT host fails the handshake
//   - Requests carrying credentials are refused, outside and inside a tunnel

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/storage/keys"
	"github.com/nauarch/phortos/internal/frontdoor/proxy"
	"github.com/nauarch/phortos/internal/kernel/clock"

	feature "github.com/nauarch/phortos/internal/features/trust"
)

const docker = "download.docker.com"

// intercepting serves a door intercepting download.docker.com and gives a client trusting the
// published bundle, as apt does with phortosctl's CaInfo, plus the door's address.
func intercepting(t *testing.T, f *fetcher) (*http.Client, string) {
	t.Helper()
	roots, addr := interception(t, &proxy.Door{Fetcher: f, Log: slog.New(slog.DiscardHandler)})
	tp := &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: addr}), TLSClientConfig: &tls.Config{RootCAs: roots}}
	t.Cleanup(tp.CloseIdleConnections)
	return &http.Client{Transport: tp}, addr
}

// interception gives door an issuer for download.docker.com and serves it, giving the CAs of the
// published bundle and the door's address.
func interception(t *testing.T, door *proxy.Door) (*x509.CertPool, string) {
	t.Helper()
	ks, err := keys.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tr, err := feature.Compose(feature.Options{Keys: ks, Hosts: []string{docker}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	for _, ca := range tr.Bundle().CAs {
		block, _ := pem.Decode([]byte(ca.PEM))
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		roots.AddCert(cert)
	}
	door.Issuer = tr
	srv := httptest.NewServer(door)
	t.Cleanup(srv.Close)
	p, _ := url.Parse(srv.URL)
	return roots, p.Host
}

// get fetches u and gives the status and body.
func get(t *testing.T, c *http.Client, u string, header ...string) (int, string, error) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

func TestAnInterceptedHostIsServedThroughATunnel(t *testing.T) {
	f := &fetcher{files: map[string][]byte{
		"https://download.docker.com/linux/debian/dists/trixie/InRelease": []byte("release"),
		"https://download.docker.com/linux/debian/a.deb?x=1":              []byte("deb"),
	}}
	c, _ := intercepting(t, f)
	code, body, err := get(t, c, "https://Download.Docker.com/linux/debian/dists/trixie/InRelease")
	if err != nil || code != 200 || body != "release" {
		t.Fatalf("%d %q %v", code, body, err)
	}
	code, body, err = get(t, c, "https://download.docker.com/linux/debian/a.deb?x=1")
	if err != nil || code != 200 || body != "deb" {
		t.Fatalf("%d %q %v", code, body, err)
	}
	if code, _, _ := get(t, c, "https://download.docker.com/linux/debian/a.deb", "Authorization", "Basic eDp5"); code != http.StatusForbidden {
		t.Errorf("credentials inside a tunnel: %d", code)
	}
}

func TestOtherConnectsAreRefused(t *testing.T) {
	c, _ := intercepting(t, &fetcher{})
	if _, _, err := get(t, c, "https://deb.debian.org/debian/InRelease"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("a host not intercepted: %v", err)
	}
	if _, _, err := get(t, c, "https://download.docker.com:8443/x"); err == nil || !strings.Contains(err.Error(), "Bad Request") {
		t.Errorf("another port: %v", err)
	}
	plain, _ := through(t, &fetcher{})
	if _, _, err := get(t, plain, "https://download.docker.com/x"); err == nil || !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("no issuer: %v", err)
	}
	if code, _, _ := get(t, plain, "http://repo.example/x", "Authorization", "Bearer t"); code != http.StatusForbidden {
		t.Errorf("credentials: %d", code)
	}
}

func TestAnotherServerNameFailsTheHandshake(t *testing.T) {
	_, addr := intercepting(t, &fetcher{})
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_, _ = io.WriteString(raw, "CONNECT download.docker.com:443 HTTP/1.1\r\nHost: download.docker.com:443\r\n\r\n")
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v %v", resp, err)
	}
	conn := tls.Client(raw, &tls.Config{ServerName: "evil.example", InsecureSkipVerify: true})
	if err := conn.Handshake(); err == nil {
		t.Error("a certificate was given for another name")
	}
}
