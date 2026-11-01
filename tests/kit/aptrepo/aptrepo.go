// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package aptrepo is an apt repository served in-process for tests, in either shape: classic
// (dists/<suite>/, by hash when asked) or flat (one folder, as NVIDIA's). It counts requests per
// path and can hold, slow down, trickle or corrupt a file, so tests can see what reached upstream.
package aptrepo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"time"
)

// Options shape the repository: flat or classic, served by hash too, how many packages, whether
// it is served over HTTPS (then as https://example.com, reached through Transport), and whether
// its release file lists SHA512 sums only (as NVIDIA's container toolkit's does).
type Options struct {
	Flat     bool
	ByHash   bool
	Packages int
	TLS      bool
	SHA512   bool
}

// TLSHost is the name an HTTPS repository is served as; the test server's certificate names it.
const TLSHost = "example.com"

// Repo is the repository and its server.
type Repo struct {
	opts       Options
	srv        *httptest.Server
	releaseDir string
	root       string
	indexDir   string

	mu       sync.Mutex
	files    map[string][]byte
	modified time.Time
	version  int
	hits     map[string]int
	slow     map[string]time.Duration
	trickle  map[string]pace
	held     map[string]*gate
	corrupt  map[string]bool
}

// pace is a trickle: n bytes every so often.
type pace struct {
	n     int
	every time.Duration
}

// New builds and serves the repository; Close stops it.
func New(o Options) *Repo {
	if o.Packages == 0 {
		o.Packages = 3
	}
	r := &Repo{opts: o, hits: map[string]int{}, slow: map[string]time.Duration{}, trickle: map[string]pace{}, held: map[string]*gate{}, corrupt: map[string]bool{}}
	if o.Flat {
		r.root, r.releaseDir, r.indexDir = "/repo/x86_64", "/repo/x86_64", "."
	} else {
		r.root, r.releaseDir, r.indexDir = "/debian", "/debian/dists/stable", "main/binary-amd64"
	}
	r.publish()
	if o.TLS {
		r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	} else {
		r.srv = httptest.NewServer(http.HandlerFunc(r.serve))
	}
	return r
}

// Transport reaches the repository: every connection goes to its server, and its certificate is
// trusted, so https://example.com works without DNS.
func (r *Repo) Transport() *http.Transport {
	tp := r.srv.Client().Transport.(*http.Transport).Clone()
	addr := r.srv.Listener.Addr().String()
	tp.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return tp
}

// Close stops the server, letting held files go.
func (r *Repo) Close() {
	r.mu.Lock()
	for _, g := range r.held {
		g.let()
	}
	r.mu.Unlock()
	r.srv.Close()
}

// gate holds a file's body until let.
type gate struct {
	ch   chan struct{}
	once sync.Once
}

// let opens the gate; again is harmless.
func (g *gate) let() { g.once.Do(func() { close(g.ch) }) }

// URL is the server's base, such as http://127.0.0.1:41234, or https://example.com over TLS.
func (r *Repo) URL() string {
	if r.opts.TLS {
		return "https://" + TLSHost
	}
	return r.srv.URL
}

// Host is the server's host and port, as a proxy request names it; example.com over TLS.
func (r *Repo) Host() string {
	if r.opts.TLS {
		return TLSHost
	}
	return strings.TrimPrefix(r.srv.URL, "http://")
}

// InRelease is the path of the inline-signed release file.
func (r *Repo) InRelease() string { return r.releaseDir + "/InRelease" }

// Index is the path of the Packages index in a compression ("", ".gz" or ".xz").
func (r *Repo) Index(ext string) string { return path.Join(r.releaseDir, r.indexDir, "Packages"+ext) }

// ByHashPath is the by-hash path of a file the repository serves.
func (r *Repo) ByHashPath(p string) string {
	return path.Join(path.Dir(p), "by-hash", "SHA256", Sum(r.File(p)))
}

// Deb is the path of package i (from 0).
func (r *Repo) Deb(i int) string { return path.Join(r.root, r.filename(i)) }

// Asked is the path apt asks for package i: the root and the file name as the index gives it,
// not cleaned, so a flat repository's "./name" stays.
func (r *Repo) Asked(i int) string { return r.root + "/" + r.filename(i) }

// File is what the repository serves at a path now.
func (r *Repo) File(p string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.files[p]
}

// Hits is how many requests reached a path.
func (r *Repo) Hits(p string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[p]
}

// Slow makes a path send half its body, wait d, then send the rest.
func (r *Repo) Slow(p string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.slow[p] = d
}

// Trickle makes a path send its body n bytes at a time, one piece every d.
func (r *Repo) Trickle(p string, n int, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trickle[p] = pace{n: n, every: d}
}

// Hold makes a path send its headers and half its body, then wait until the returned function is
// called (or the repository closes).
func (r *Repo) Hold(p string) (let func()) {
	g := &gate{ch: make(chan struct{})}
	r.mu.Lock()
	r.held[p] = g
	r.mu.Unlock()
	return g.let
}

// Corrupt makes a path serve its bytes with the last one changed.
func (r *Repo) Corrupt(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.corrupt[p] = true
}

// Publish makes a new version: every package changes, so the indexes and the release file do.
func (r *Repo) Publish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version++
	r.publishLocked()
}

// Sum is the SHA256 of b in hex.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Sum512 is the SHA512 of b in hex.
func Sum512(b []byte) string {
	s := sha512.Sum512(b)
	return hex.EncodeToString(s[:])
}

// publish builds the first version.
func (r *Repo) publish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.publishLocked()
}

// publishLocked builds the packages, the indexes and the release file of the current version.
func (r *Repo) publishLocked() {
	r.files = map[string][]byte{}
	r.modified = time.Date(2026, 10, 30, 6, 0, r.version, 0, time.UTC)
	var idx bytes.Buffer
	for i := range r.opts.Packages {
		deb := bytes.Repeat([]byte(fmt.Sprintf("package %d version %d;", i, r.version)), 2000+i)
		r.files[r.Deb(i)] = deb
		fmt.Fprintf(&idx, "Package: pkg%d\nVersion: %d\nArchitecture: amd64\nFilename: %s\nSize: %d\nSHA256: %s\nDescription: test package\n long text\n\n",
			i, r.version, r.filename(i), len(deb), Sum(deb))
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(idx.Bytes())
	_ = w.Close()
	indexes := map[string][]byte{"Packages": idx.Bytes(), "Packages.gz": gz.Bytes(), "Packages.xz": append([]byte("\xfd7zXZ"), idx.Bytes()...)}
	var rel strings.Builder
	section, sum := "SHA256", Sum
	if r.opts.SHA512 {
		section, sum = "SHA512", Sum512
	}
	fmt.Fprintf(&rel, "Origin: test\nSuite: stable\nAcquire-By-Hash: %s\n%s:\n", map[bool]string{true: "yes", false: "no"}[r.opts.ByHash], section)
	for name, body := range indexes {
		rp := path.Join(r.indexDir, name)
		p := path.Join(r.releaseDir, rp)
		r.files[p] = body
		if r.opts.ByHash {
			r.files[path.Join(path.Dir(p), "by-hash", section, sum(body))] = body
		}
		fmt.Fprintf(&rel, " %s %d %s\n", sum(body), len(body), rp)
	}
	r.files[r.releaseDir+"/Release"] = []byte(rel.String())
	r.files[r.InRelease()] = []byte("-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" + rel.String() +
		"-----BEGIN PGP SIGNATURE-----\n\nnot a real signature\n-----END PGP SIGNATURE-----\n")
}

// filename is package i's file name as the index gives it.
func (r *Repo) filename(i int) string {
	if r.opts.Flat {
		return fmt.Sprintf("./pkg%d_1_amd64.deb", i)
	}
	return fmt.Sprintf("pool/main/p/pkg%d/pkg%d_1_amd64.deb", i, i)
}

// serve answers a GET for a file, with 304 when unchanged since If-Modified-Since.
func (r *Repo) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	p := req.URL.Path
	r.hits[p]++
	body, ok := r.files[p]
	modified, slow, held, corrupt, trickle := r.modified, r.slow[p], r.held[p], r.corrupt[p], r.trickle[p]
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	if since, err := http.ParseTime(req.Header.Get("If-Modified-Since")); err == nil && !modified.After(since) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if corrupt {
		body = append(bytes.Clone(body[:len(body)-1]), body[len(body)-1]^0xff)
	}
	w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if trickle.n > 0 {
		for i := 0; i < len(body); i += trickle.n {
			if _, err := w.Write(body[i:min(i+trickle.n, len(body))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(trickle.every)
		}
		return
	}
	half := len(body) / 2
	_, _ = w.Write(body[:half])
	w.(http.Flusher).Flush()
	switch {
	case held != nil:
		<-held.ch
	case slow > 0:
		time.Sleep(slow)
	}
	_, _ = w.Write(body[half:])
}
