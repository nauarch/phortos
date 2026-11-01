// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package proxy_test

// Verifies, with requests queued on one connection the way apt pipelines them:
//   - Every queued GET for an http:// URL is handed to the prefetcher before the server gets to
//     it, with the door's name for it (":80" dropped); a POST, a GET carrying credentials and a
//     request that is no proxy request are not; the server still answers each request, in order
//   - The server's idle and header timeouts still end a connection read ahead
//   - Inside a tunnel the queued GETs are prefetched as https:// URLs of the tunnel's host

import (
	"bufio"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/frontdoor/proxy"
)

// recorder records the URLs it is asked to prefetch.
type recorder struct {
	mu   sync.Mutex
	urls []string
}

func (r *recorder) Prefetch(u string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, u)
	return true
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.urls)
}

// serveAhead serves h on a listener read ahead into p, configured by tune, and gives its address.
func serveAhead(t *testing.T, p *recorder, h http.Handler, tune func(*http.Server)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	if tune != nil {
		tune(srv)
	}
	go func() { _ = srv.Serve(proxy.Listener(ln, p)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// dial connects to addr, giving up on reads after a while.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c
}

// answer reads one response and gives its status and body.
func answer(t *testing.T, br *bufio.Reader) (int, string) {
	t.Helper()
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, string(b)
}

func TestQueuedRequestsArePrefetchedBeforeTheirTurn(t *testing.T) {
	p := &recorder{}
	addr := serveAhead(t, p, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(150 * time.Millisecond)
		_, _ = io.WriteString(w, r.Method+" "+r.URL.String())
	}), nil)
	c := dial(t, addr)
	queue := []string{
		"GET http://deb.debian.org/a.deb HTTP/1.1\r\nHost: deb.debian.org\r\n\r\n",
		"GET http://DEB.debian.org:80/b.deb HTTP/1.1\r\nHost: deb.debian.org\r\n\r\n",
		"POST http://deb.debian.org/c HTTP/1.1\r\nHost: deb.debian.org\r\nContent-Length: 3\r\n\r\nxyz",
		"GET http://deb.debian.org/d.deb HTTP/1.1\r\nHost: deb.debian.org\r\nAuthorization: Basic eDp5\r\n\r\n",
		"GET /healthz HTTP/1.1\r\nHost: phortos\r\n\r\n",
		"GET http://deb.debian.org/e.deb HTTP/1.1\r\nHost: deb.debian.org\r\n\r\n",
	}
	if _, err := io.WriteString(c, strings.Join(queue, "")); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	answer(t, br)
	want := []string{"http://deb.debian.org/a.deb", "http://deb.debian.org/b.deb", "http://deb.debian.org/e.deb"}
	if got := p.seen(); !slices.Equal(got, want) {
		t.Errorf("prefetched by the first answer: %q", got)
	}
	for _, body := range []string{"GET http://DEB.debian.org:80/b.deb", "POST http://deb.debian.org/c", "GET http://deb.debian.org/d.deb", "GET /healthz", "GET http://deb.debian.org/e.deb"} {
		if code, got := answer(t, br); code != http.StatusOK || got != body {
			t.Errorf("out of order: %d %q, want %q", code, got, body)
		}
	}
}

func TestTheServersTimeoutsStillHold(t *testing.T) {
	addr := serveAhead(t, &recorder{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), func(s *http.Server) { s.IdleTimeout, s.ReadHeaderTimeout = 200*time.Millisecond, 200*time.Millisecond })
	idle := dial(t, addr)
	_, _ = io.WriteString(idle, "GET http://deb.debian.org/a HTTP/1.1\r\nHost: deb.debian.org\r\n\r\n")
	br := bufio.NewReader(idle)
	answer(t, br)
	start := time.Now()
	if _, err := br.ReadByte(); err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("an idle connection was not closed in time: %v after %v", err, time.Since(start))
	}
	partial := dial(t, addr)
	_, _ = io.WriteString(partial, "GET http://deb.debian.org/a HTTP/1.1\r\nHost: deb")
	start = time.Now()
	if _, err := io.ReadAll(partial); err != nil || time.Since(start) > 3*time.Second {
		t.Errorf("a request cut short was not given up in time: %v after %v", err, time.Since(start))
	}
}

func TestRequestsInATunnelArePrefetchedAsHTTPS(t *testing.T) {
	p := &recorder{}
	f := &fetcher{files: map[string][]byte{"https://" + docker + "/x.deb": []byte("x"), "https://" + docker + "/y.deb": []byte("y")}}
	roots, addr := interception(t, &proxy.Door{Fetcher: f, Prefetcher: p, Log: slog.New(slog.DiscardHandler)})
	c := dial(t, addr)
	_, _ = io.WriteString(c, "CONNECT "+docker+":443 HTTP/1.1\r\nHost: "+docker+":443\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	tc := tls.Client(c, &tls.Config{RootCAs: roots, ServerName: docker})
	_, _ = io.WriteString(tc, "GET /x.deb HTTP/1.1\r\nHost: "+docker+"\r\n\r\nGET /y.deb HTTP/1.1\r\nHost: "+docker+"\r\n\r\n")
	br := bufio.NewReader(tc)
	for _, want := range []string{"x", "y"} {
		if code, got := answer(t, br); code != http.StatusOK || got != want {
			t.Errorf("%d %q, want %q", code, got, want)
		}
	}
	if got := p.seen(); !slices.Equal(got, []string{"https://" + docker + "/x.deb", "https://" + docker + "/y.deb"}) {
		t.Errorf("prefetched %q", got)
	}
}
