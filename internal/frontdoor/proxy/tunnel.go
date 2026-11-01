// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// handshakeWithin bounds the TLS handshake inside a tunnel.
const handshakeWithin = 10 * time.Second

// tunnel answers CONNECT for a host phortos intercepts: it ends TLS with the issuer's certificate
// for that host and serves the requests inside from the cache, as https:// URLs. Only port 443 is
// accepted, only for intercepted hosts, and the TLS server name must be the CONNECT host. Any
// other CONNECT is refused, so apt goes direct for it.
func (d *Door) tunnel(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || port != "443" {
		http.Error(w, "phortos: CONNECT is accepted for port 443 only", http.StatusBadRequest)
		return
	}
	host = strings.ToLower(host)
	if d.Issuer == nil || !d.Issuer.Intercepts(host) || !d.Fetcher.Serves(host) {
		http.Error(w, "phortos: "+host+" is not intercepted; reach it directly", http.StatusForbidden)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "phortos: cannot take over the connection", http.StatusInternalServerError)
		return
	}
	raw, buf, err := hj.Hijack()
	if err != nil {
		d.Log.Warn("CONNECT not taken over", "client", r.RemoteAddr, "err", err)
		return
	}
	if _, err := raw.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
		_ = raw.Close()
		return
	}
	conn := tls.Server(&buffered{Conn: raw, r: buf.Reader}, &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.ServerName != "" && !strings.EqualFold(hello.ServerName, host) {
				return nil, fmt.Errorf("server name %s is not the CONNECT host %s", hello.ServerName, host)
			}
			return d.Issuer.Certificate(host)
		},
	})
	_ = conn.SetDeadline(time.Now().Add(handshakeWithin))
	if err := conn.Handshake(); err != nil {
		d.Log.Warn("TLS handshake failed", "client", r.RemoteAddr, "host", host, "err", err)
		_ = conn.Close()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	inner := &http.Server{
		Handler:           http.HandlerFunc(func(w2 http.ResponseWriter, r2 *http.Request) { d.inside(w2, r2, host) }),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	var served net.Conn = conn
	if d.Prefetcher != nil {
		served = readAhead(conn, "https://"+host, d.Prefetcher)
	}
	_ = inner.Serve(&single{conn: served, done: make(chan struct{})})
}

// inside answers one request inside a tunnel to host.
func (d *Door) inside(w http.ResponseWriter, r *http.Request, host string) {
	if refuseCredentials(w, r) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "phortos: only GET and HEAD", http.StatusMethodNotAllowed)
		return
	}
	d.serve(w, r, "https://"+host+r.URL.RequestURI())
}

// buffered is a hijacked connection whose first bytes may already sit in the server's reader.
type buffered struct {
	net.Conn
	r *bufio.Reader
}

// Read reads what was buffered first.
func (b *buffered) Read(p []byte) (int, error) { return b.r.Read(p) }

// single is a listener handing out one connection, then waiting until it is closed, so an
// http.Server serves a tunnel for as long as it lives.
type single struct {
	conn   net.Conn
	done   chan struct{}
	handed bool
}

// Accept hands out the connection once, then blocks until it closes.
func (s *single) Accept() (net.Conn, error) {
	if !s.handed {
		s.handed = true
		return &closing{Conn: s.conn, done: s.done, once: new(sync.Once)}, nil
	}
	<-s.done
	return nil, net.ErrClosed
}

// Close is a no-op: the connection closes itself.
func (s *single) Close() error { return nil }

// Addr is the connection's local address.
func (s *single) Addr() net.Addr { return s.conn.LocalAddr() }

// closing tells its listener when it closes.
type closing struct {
	net.Conn
	done chan struct{}
	once *sync.Once
}

// Close closes the connection and wakes the listener.
func (c *closing) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}
