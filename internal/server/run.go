// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/features/cache"
	"github.com/nauarch/phortos/internal/features/trust"
	opsdoor "github.com/nauarch/phortos/internal/frontdoor/ops"
	"github.com/nauarch/phortos/internal/frontdoor/proxy"
	"github.com/nauarch/phortos/internal/kernel/bytesize"
)

// shutdownGrace is how long running answers get to finish once phortos is told to stop.
const shutdownGrace = 10 * time.Second

// upstreamTimeout bounds connecting to a repository and waiting for its answer's headers.
const upstreamTimeout = 30 * time.Second

// Run composes phortos (the store and its lock, the upstream client, the cache, the authority
// when hosts are intercepted, and the doors) and serves until ctx ends. Downloads run on ctx too,
// so they stop with it.
func Run(ctx context.Context, o Options, log *slog.Logger) error {
	o, err := Defaults(o)
	if err != nil {
		return err
	}
	store, err := disk.Open(disk.Options{Root: o.Dir, Init: o.Init})
	if err != nil {
		return err
	}
	release, err := store.Lock(disk.LockOptions{Owner: o.Owner, Host: o.Host, PID: o.PID})
	if err != nil {
		return err
	}
	defer release()
	ctx, stopAll := context.WithCancel(ctx)
	defer stopAll()
	up := upstream.New(upstream.Options{Resolver: o.Resolver, Timeout: upstreamTimeout, UserAgent: "phortos/" + o.Version, Transport: o.Transport})
	c, err := cache.Compose(cache.Options{
		Store: store, Upstream: up, Hosts: o.Hosts, ReleaseTTL: o.ReleaseTTL, ReleaseWithin: o.ReleaseWithin,
		StallAfter: o.StallAfter, MinRate: o.MinRate, MaxBytes: o.MaxBytes, EvictEvery: o.EvictEvery, Background: ctx, Log: log,
	})
	if err != nil {
		return err
	}
	if err := c.Recall(); err != nil {
		log.Warn("kept releases not recalled", "err", err)
	}
	go c.Maintain(ctx)
	ln, err := listen(o.Addr)
	if err != nil {
		return err
	}
	var tr *trust.Trust
	var servers []*serving
	if o.Keys != "" {
		var apiSrv *serving
		if tr, apiSrv, err = serveAPI(ctx, o, ln.Addr(), log); err != nil {
			_ = ln.Close()
			return err
		}
		servers = append(servers, apiSrv)
	} else {
		log.Info("no key folder (--keys): no pinned API, so hosts cannot be set up from phortos and no HTTPS repository is intercepted")
	}
	srv := &http.Server{Handler: Handler(c, tr, o.Version, log), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	servers = append(servers, &serving{srv: srv, ln: proxy.Listener(ln, c)})
	log.Info("phortos serving", "version", o.Version, "addr", ln.Addr().String(), "store", o.Dir, "cap", capOf(o.MaxBytes), "hosts", c.Hosts())
	if o.Listening != nil {
		o.Listening(ln.Addr().String())
	}
	_ = o.Notify("READY=1")
	if o.Reload != nil {
		every := o.ReloadEvery
		if every == 0 {
			every = DefaultReloadEvery
		}
		r := &reloader{running: o, cache: c, trust: tr, log: log}
		go r.run(ctx, o.Reload, every, o.ReloadNow)
	}
	served := make(chan error, len(servers))
	for _, s := range servers {
		go func() { served <- s.serve() }()
	}
	unanswered := make(chan error, 1)
	if o.SelfCheck > 0 {
		go func() {
			unanswered <- Watch(ctx, ln.Addr(), o.SelfCheck, func() { _ = o.Notify("WATCHDOG=1") }, log)
		}()
	}
	var failed error
	select {
	case failed = <-served:
	case failed = <-unanswered:
		if failed != nil {
			// Stopping may hang on what hangs phortos: give it the grace, then end the process.
			time.AfterFunc(shutdownGrace, func() {
				log.Error("phortos did not stop in time after its self-check; exiting", "err", failed)
				o.Exit(1)
			})
		}
	case <-ctx.Done():
	}
	stopAll()
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	for _, s := range servers {
		if err := s.srv.Shutdown(shutdown); err != nil && !errors.Is(err, context.DeadlineExceeded) && failed == nil {
			failed = err
		}
	}
	return failed
}

// capOf spells the store's cap for the log.
func capOf(max int64) string {
	if max == 0 {
		return "none"
	}
	return bytesize.Format(max)
}

// serving is one server and the listener it serves on, with TLS or without.
type serving struct {
	srv *http.Server
	ln  net.Listener
	tls bool
}

// serve serves until the server shuts down, which is not a failure.
func (s *serving) serve() error {
	var err error
	if s.tls {
		err = s.srv.ServeTLS(s.ln, "", "")
	} else {
		err = s.srv.Serve(s.ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// listen listens on addr.
func listen(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return ln, nil
}

// closeAll closes the listeners of servers not yet serving.
func closeAll(servers []*serving) {
	for _, s := range servers {
		_ = s.ln.Close()
	}
}

// Handler routes proxy requests (an absolute URL, or CONNECT) to the proxy door and the rest to
// the ops door. tr, when not nil, lets the proxy door intercept HTTPS and routing name its CA.
func Handler(c *cache.Cache, tr *trust.Trust, version string, log *slog.Logger) http.Handler {
	door := &proxy.Door{Fetcher: c, Prefetcher: c, Log: log}
	opts := opsdoor.Options{Reporter: c, Fetcher: c, Version: version, Log: log}
	if tr != nil {
		door.Issuer, opts.Trust = tr, tr
	}
	own := opsdoor.New(opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() {
			door.ServeHTTP(w, r)
			return
		}
		own.ServeHTTP(w, r)
	})
}
