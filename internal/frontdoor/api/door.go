// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package api is phortos' front door for its clients: served over TLS with the identity's
// certificate, which clients pin, it publishes the trust bundle apt installs.
package api

import (
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/nauarch/phortos/internal/contracts/trust"
)

// Door serves the API from a trust.Publisher.
type Door struct {
	Publisher trust.Publisher
	Log       *slog.Logger
}

// Handler routes the API: GET /trust gives the bundle as JSON, GET /healthz answers ok.
func (d *Door) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /trust", d.bundle)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	return mux
}

// TLS is the TLS setup the API is served with: the identity's certificate, TLS 1.2 at least.
func (d *Door) TLS() *tls.Config {
	id := d.Publisher.Identity()
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{id}}
}

// bundle answers the trust bundle.
func (d *Door) bundle(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(d.Publisher.Bundle()); err != nil {
		d.Log.Warn("trust bundle not sent", "err", err)
	}
}
