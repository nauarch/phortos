// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/nauarch/phortos/internal/adapters/storage/keys"
	"github.com/nauarch/phortos/internal/features/trust"
	"github.com/nauarch/phortos/internal/frontdoor/api"
	"github.com/nauarch/phortos/internal/frontdoor/setup"
)

// serveAPI composes the authority over the key folder (the identity, and working CAs for the
// intercepted hosts, rotated until ctx ends) and the API server, already listening, publishing
// the bundle and the setup script for hosts reaching the proxy at proxy.
func serveAPI(ctx context.Context, o Options, proxy net.Addr, log *slog.Logger) (*trust.Trust, *serving, error) {
	ks, err := keys.Open(o.Keys)
	if err != nil {
		return nil, nil, err
	}
	tr, err := trust.Compose(trust.Options{Keys: ks, Hosts: o.Intercept, Log: log})
	if err != nil {
		return nil, nil, err
	}
	ln, err := listen(o.APIAddr)
	if err != nil {
		return nil, nil, err
	}
	go tr.Maintain(ctx, log)
	door := &api.Door{Publisher: tr, Log: log}
	mux := http.NewServeMux()
	mux.Handle("/", door.Handler())
	setupDoor := setup.New(setup.Options{Files: o.Tools, Pin: tr.Pin(), ProxyPort: portOf(proxy), Version: o.Version, Log: log})
	mux.Handle("/setup", setupDoor)
	mux.Handle("/get/", setupDoor)
	srv := &http.Server{Handler: mux, TLSConfig: door.TLS(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	name := o.Advertise
	if name == "" {
		name = o.Host
	}
	apiAddr := net.JoinHostPort(name, portOf(ln.Addr()))
	log.Info("phortos API serving", "addr", ln.Addr().String(), "pin", tr.Pin(), "intercepted", tr.Bundle().Hosts)
	log.Info("set a host up with", "command", setup.Command(apiAddr, tr.Pin()))
	if o.APIListening != nil {
		o.APIListening(ln.Addr().String())
	}
	return tr, &serving{srv: srv, ln: ln, tls: true}, nil
}

// portOf is the port of a listening address.
func portOf(a net.Addr) string {
	if tcp, ok := a.(*net.TCPAddr); ok {
		return fmt.Sprint(tcp.Port)
	}
	_, port, _ := net.SplitHostPort(a.String())
	return port
}
