// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package upstream fetches files from the real repositories over HTTP and HTTPS, with full
// certificate checks, bytes exactly as sent (no transparent decompression) and no redirect from
// https down to http.
package upstream

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	contract "github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// defaultTimeout bounds connecting and waiting for an answer's headers, not reading its body.
const defaultTimeout = 30 * time.Second

// maxRedirects is how many redirects one fetch follows.
const maxRedirects = 10

// errDowngrade refuses a redirect from https to http, which would undo the TLS the client asked
// for.
var errDowngrade = errors.New("redirect from https to http refused")

// Options say how repositories are reached: the timeout for connecting and for an answer's
// headers (zero takes 30 s), a DNS server ("10.0.0.53:53") to resolve with instead of the
// system's, the User-Agent, and the transport (nil builds one; tests pass their own).
type Options struct {
	Timeout   time.Duration
	Resolver  string
	UserAgent string
	Transport http.RoundTripper
}

// HTTP is the real repositories, reached over HTTP.
type HTTP struct {
	client    *http.Client
	userAgent string
}

var _ contract.Upstream = (*HTTP)(nil)

// New builds the client.
func New(o Options) *HTTP {
	transport := o.Transport
	if transport == nil {
		timeout := cmp.Or(o.Timeout, defaultTimeout)
		dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Resolver: resolver(o.Resolver)}
		// HTTP/1.1 only, a connection per download, as apt itself: over HTTP/2 every download
		// from a host shares one connection, which a repository may throttle (Launchpad's PPAs:
		// six packages at once took 24 s over HTTP/2, 2 s over HTTP/1.1), and which, when it
		// stalls or drops, fails every download on it at once.
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			Protocols:             protocols,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
		}
	}
	return &HTTP{
		client:    &http.Client{Transport: transport, CheckRedirect: checkRedirect},
		userAgent: cmp.Or(o.UserAgent, "phortos"),
	}
}

// checkRedirect follows at most maxRedirects redirects and never from https to http.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("more than %d redirects", maxRedirects)
	}
	if req.URL.Scheme != "https" && via[len(via)-1].URL.Scheme == "https" {
		return errDowngrade
	}
	return nil
}

// Fetch GETs url.
func (h *HTTP) Fetch(ctx context.Context, url string, c contract.Conditions) (contract.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return contract.Response{}, fmt.Errorf("%s: %w", url, errkind.ErrInvalid)
	}
	req.Header.Set("User-Agent", h.userAgent)
	if !c.Modified.IsZero() {
		req.Header.Set("If-Modified-Since", c.Modified.UTC().Format(http.TimeFormat))
	}
	if c.ETag != "" {
		req.Header.Set("If-None-Match", c.ETag)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return contract.Response{}, ctx.Err()
		}
		if errors.Is(err, errDowngrade) {
			return contract.Response{}, fmt.Errorf("GET %s: %w: %w", url, errkind.ErrUpstream, err)
		}
		return contract.Response{}, fmt.Errorf("GET %s: %w: %w", url, errkind.ErrUnreachable, err)
	}
	modified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return contract.Response{
		Status:      resp.StatusCode,
		Size:        resp.ContentLength,
		Modified:    modified,
		ETag:        resp.Header.Get("ETag"),
		ContentType: resp.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}

// resolver looks hosts up at server, or with the system's resolver when server is empty.
func resolver(server string) *net.Resolver {
	if server == "" {
		return nil
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		},
	}
}
