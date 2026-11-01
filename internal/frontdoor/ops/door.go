// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package ops is phortos' front door for operators and for phortosctl's detect, on the proxy's
// own address: GET /healthz, GET /metrics and GET /api/route.
package ops

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/trust"
)

// Trust is what routing needs of the authority: which hosts it intercepts and its working CAs.
type Trust interface {
	Intercepts(host string) bool
	Bundle() trust.Bundle
}

// Options are what the door serves from: the health and counters to report, the fetcher whose
// hosts are proxied, the authority when HTTPS hosts are intercepted (nil otherwise), the version
// phortos was built as, and where it logs.
type Options struct {
	Reporter ops.Reporter
	Fetcher  repo.Fetcher
	Trust    Trust
	Version  string
	Log      *slog.Logger
}

// door serves phortos' operations.
type door struct {
	o Options
}

// New builds the door.
func New(o Options) http.Handler {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	d := &door{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", d.healthz)
	mux.HandleFunc("GET /metrics", d.metrics)
	mux.HandleFunc("GET /api/route", d.route)
	return mux
}

// healthz answers whether phortos can serve: 200, or 503 with why.
func (d *door) healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if err := d.o.Reporter.Healthy(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(err.Error() + "\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}

// reply writes v as JSON with status.
func (d *door) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		d.o.Log.Debug("answer not written", "err", err)
	}
}
