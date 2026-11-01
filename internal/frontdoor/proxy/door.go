// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package proxy is phortos' front door for apt: an HTTP proxy that answers GET and HEAD requests
// for absolute URLs ("GET http://deb.debian.org/debian/… HTTP/1.1") from the cache, and CONNECT
// for the HTTPS hosts it intercepts, ending TLS itself.
package proxy

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Door serves proxy requests from a repo.Fetcher. Issuer, when set, gives the certificates for
// the HTTPS hosts phortos intercepts; without it no CONNECT is accepted. Prefetcher, when set,
// is handed the requests queued inside a tunnel ahead of their turn (see ahead); Listener does so
// for the connections the door is served on.
type Door struct {
	Fetcher    repo.Fetcher
	Prefetcher repo.Prefetcher
	Issuer     trust.Issuer
	Log        *slog.Logger
}

// ServeHTTP answers one proxy request.
func (d *Door) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if refuseCredentials(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodConnect:
		d.tunnel(w, r)
		return
	case !r.URL.IsAbs():
		http.Error(w, "phortos: a proxy request names an absolute URL", http.StatusBadRequest)
		return
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "phortos: only GET and HEAD", http.StatusMethodNotAllowed)
		return
	case r.URL.Scheme != "http":
		http.Error(w, "phortos: only http:// URLs are proxied", http.StatusBadRequest)
		return
	}
	u := *r.URL
	u.Host = defaultPort(strings.ToLower(u.Host))
	d.serve(w, r, u.String())
}

// refuseCredentials answers 403 to a request carrying credentials: phortos never handles a
// repository that needs them, so they never pass through it.
func refuseCredentials(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Authorization") == "" && r.Header.Get("Proxy-Authorization") == "" {
		return false
	}
	http.Error(w, "phortos: requests carrying credentials are not proxied; reach this repository directly", http.StatusForbidden)
	return true
}

// serve answers a GET or HEAD for url from the cache.
func (d *Door) serve(w http.ResponseWriter, r *http.Request, url string) {
	ask := repo.Ask{}
	if t, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil {
		ask.Modified = t
	}
	start := time.Now()
	f, err := d.Fetcher.Get(r.Context(), url, ask)
	if err != nil {
		code := status(err)
		d.Log.Warn("request failed", "client", r.RemoteAddr, "url", url, "status", code, "err", err)
		http.Error(w, "phortos: "+err.Error(), code)
		return
	}
	defer f.Body.Close()
	d.write(w, r, f)
	d.Log.Debug("served", "client", r.RemoteAddr, "url", url, "source", f.Source, "status", f.Status, "took", time.Since(start))
}

// write sends a file: through ServeContent when it seeks (ranges, If-Modified-Since and HEAD for
// free), else as it streams.
func (d *Door) write(w http.ResponseWriter, r *http.Request, f repo.File) {
	h := w.Header()
	h.Set("X-Phortos", string(f.Source))
	if f.ETag != "" {
		h.Set("ETag", f.ETag)
	}
	if f.ContentType != "" {
		h.Set("Content-Type", f.ContentType)
	} else {
		h.Set("Content-Type", "application/octet-stream")
	}
	if rs, ok := f.Body.(io.ReadSeeker); ok && f.Status == http.StatusOK {
		http.ServeContent(w, r, "", f.Modified, rs)
		return
	}
	if !f.Modified.IsZero() {
		h.Set("Last-Modified", f.Modified.UTC().Format(http.TimeFormat))
	}
	if f.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	}
	w.WriteHeader(f.Status)
	if r.Method == http.MethodHead || f.Status == http.StatusNotModified {
		return
	}
	if _, err := io.Copy(w, f.Body); err != nil && !errors.Is(err, r.Context().Err()) {
		d.Log.Warn("response cut short", "client", r.RemoteAddr, "url", r.URL.String(), "err", err)
	}
}

// defaultPort drops ":80" from an http host, so one repository has one name.
func defaultPort(host string) string { return strings.TrimSuffix(host, ":80") }

// status is the HTTP status a failure stands for.
func status(err error) int {
	switch errkind.Of(err) {
	case errkind.Forbidden:
		return http.StatusForbidden
	case errkind.Invalid:
		return http.StatusBadRequest
	case errkind.NotFound:
		return http.StatusNotFound
	case errkind.Unreachable:
		return http.StatusGatewayTimeout
	default:
		return http.StatusBadGateway
	}
}
