// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package api_test

// Verifies:
//   - GET /trust gives the publisher's bundle as JSON, never to be cached
//   - The TLS setup presents the publisher's identity, TLS 1.2 at least
//   - Other paths and methods are refused

import (
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/frontdoor/api"
)

// publisher publishes a fixed bundle and identity.
type publisher struct {
	bundle trust.Bundle
	id     tls.Certificate
}

// Bundle is the fixed bundle.
func (p publisher) Bundle() trust.Bundle { return p.bundle }

// Identity is the fixed identity.
func (p publisher) Identity() tls.Certificate { return p.id }

func TestTheBundleIsPublished(t *testing.T) {
	want := trust.Bundle{Pin: "sha256//abc", Hosts: []string{"download.docker.com"}, CAs: []trust.CA{
		{PEM: "-----BEGIN CERTIFICATE-----\n", NotAfter: time.Date(2026, 11, 29, 12, 0, 0, 0, time.UTC), Signature: "c2ln"},
	}}
	d := &api.Door{Publisher: publisher{bundle: want}, Log: slog.New(slog.DiscardHandler)}
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/trust")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got trust.Bundle
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Pin != want.Pin || len(got.CAs) != 1 || !got.CAs[0].NotAfter.Equal(want.CAs[0].NotAfter) || got.CAs[0].Signature != "c2ln" {
		t.Errorf("%+v", got)
	}
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("%v", resp.Header)
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/trust"}, {http.MethodGet, "/other"}} {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s: %d", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestTheIdentityIsPresented(t *testing.T) {
	id := tls.Certificate{Certificate: [][]byte{[]byte("der")}}
	cfg := (&api.Door{Publisher: publisher{id: id}}).TLS()
	if len(cfg.Certificates) != 1 || string(cfg.Certificates[0].Certificate[0]) != "der" || cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("%+v", cfg)
	}
}
