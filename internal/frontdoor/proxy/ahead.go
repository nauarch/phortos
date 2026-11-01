// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nauarch/phortos/internal/contracts/repo"
)

// maxAhead is how much a connection reads ahead of the server serving it: room for well over a
// hundred queued requests, and no more.
const maxAhead = 16 << 10

// Listener is ln with every connection read ahead (see ahead), its GET requests for http:// URLs
// handed to p; with p nil it is ln.
func Listener(ln net.Listener, p repo.Prefetcher) net.Listener {
	if p == nil {
		return ln
	}
	return &aheadListener{Listener: ln, p: p}
}

// aheadListener reads its connections ahead.
type aheadListener struct {
	net.Listener
	p repo.Prefetcher
}

// Accept reads the next connection ahead.
func (l *aheadListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return readAhead(c, "", l.p), nil
}

// ahead is a connection read ahead of the HTTP server serving it. apt queues several requests on
// one connection (HTTP/1.1 pipelining), and the server answers them one after another, so a file
// further down the queue would only be fetched once everything before it was sent; read ahead,
// each queued GET is handed to the prefetcher as soon as it arrives. The server still reads every
// byte, in order, and answers in order: reading ahead only starts downloads early.
//
// Its read deadline is its own, not the connection's: the server's timeouts, and the deadline in
// the past it sets to stop a read, end the server's read without ending the reading ahead.
type ahead struct {
	net.Conn
	mu       sync.Mutex
	buf      []byte
	err      error
	closed   bool
	deadline time.Time
	// changed is closed, and replaced, whenever any of the above changes.
	changed chan struct{}
}

// readAhead reads c ahead, handing p the URL of each GET: request-targets taken as they are
// (absolute http:// URLs, as a proxy is asked) when base is "", else base followed by the
// request's path (inside a tunnel, base is "https://host").
func readAhead(c net.Conn, base string, p repo.Prefetcher) *ahead {
	a := &ahead{Conn: c, changed: make(chan struct{})}
	r, w := io.Pipe()
	go a.fill(w)
	go queued(r, base, p)
	return a
}

// fill reads from the client until it fails or the connection closes, keeping what it read for
// the server and copying it to the request reader.
func (a *ahead) fill(w *io.PipeWriter) {
	chunk := make([]byte, 4<<10)
	for {
		if !a.room() {
			_ = w.CloseWithError(net.ErrClosed)
			return
		}
		n, err := a.Conn.Read(chunk)
		if n > 0 {
			a.mu.Lock()
			a.buf = append(a.buf, chunk[:n]...)
			a.wake()
			a.mu.Unlock()
			_, _ = w.Write(chunk[:n])
		}
		if err != nil {
			a.mu.Lock()
			a.err = err
			a.wake()
			a.mu.Unlock()
			_ = w.CloseWithError(err)
			return
		}
	}
}

// room waits until the server has read enough to read further ahead, and says whether the
// connection is still open.
func (a *ahead) room() bool {
	for {
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return false
		}
		if len(a.buf) < maxAhead {
			a.mu.Unlock()
			return true
		}
		changed := a.changed
		a.mu.Unlock()
		<-changed
	}
}

// Read gives the server what the client sent, in order, waiting for it until the read deadline.
func (a *ahead) Read(p []byte) (int, error) {
	for {
		a.mu.Lock()
		if len(a.buf) > 0 {
			n := copy(p, a.buf)
			a.buf = a.buf[n:]
			a.wake()
			a.mu.Unlock()
			return n, nil
		}
		if a.err != nil {
			err := a.err
			a.mu.Unlock()
			return 0, err
		}
		if a.closed {
			a.mu.Unlock()
			return 0, net.ErrClosed
		}
		deadline, changed := a.deadline, a.changed
		a.mu.Unlock()
		if !deadline.IsZero() {
			wait := time.Until(deadline)
			if wait <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			t := time.NewTimer(wait)
			select {
			case <-changed:
			case <-t.C:
			}
			t.Stop()
			continue
		}
		<-changed
	}
}

// SetReadDeadline sets the deadline for the server's reads.
func (a *ahead) SetReadDeadline(t time.Time) error {
	a.mu.Lock()
	a.deadline = t
	a.wake()
	a.mu.Unlock()
	return nil
}

// SetDeadline sets the deadline for the server's reads and the connection's writes.
func (a *ahead) SetDeadline(t time.Time) error {
	_ = a.SetReadDeadline(t)
	return a.Conn.SetWriteDeadline(t)
}

// Close closes the connection, which ends the reading ahead.
func (a *ahead) Close() error {
	a.mu.Lock()
	a.closed = true
	a.wake()
	a.mu.Unlock()
	return a.Conn.Close()
}

// wake tells everyone waiting that something changed; a.mu is held.
func (a *ahead) wake() {
	close(a.changed)
	a.changed = make(chan struct{})
}

// queued reads the requests the client sent and hands each GET's URL to p, until the stream ends,
// a request cannot be read, or a CONNECT turns the rest into TLS. It then reads on to the end
// without looking, so the reading ahead never waits on it.
func queued(r *io.PipeReader, base string, p repo.Prefetcher) {
	br := bufio.NewReader(r)
	defer func() { _, _ = io.Copy(io.Discard, br) }()
	for {
		req, err := http.ReadRequest(br)
		if err != nil || req.Method == http.MethodConnect {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("Proxy-Authorization") != "" {
			continue
		}
		if u := target(req, base); u != "" {
			p.Prefetch(u)
		}
	}
}

// target is the URL a GET asks for, as the door names it; "" for one the door does not proxy.
func target(req *http.Request, base string) string {
	if base != "" {
		return base + req.URL.RequestURI()
	}
	if !req.URL.IsAbs() || req.URL.Scheme != "http" {
		return ""
	}
	u := *req.URL
	u.Host = defaultPort(strings.ToLower(u.Host))
	return u.String()
}
