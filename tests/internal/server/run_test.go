// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server_test

// Verifies:
//   - phortos serves proxy requests for a repository end to end and answers /healthz, then stops
//     when told to, releasing its store
//   - Without a store folder it refuses to start
//   - A second phortos on the same store is refused while the first runs
//   - Packages apt queues behind each other on one connection, on a cold cache, are fetched at
//     once, not one upstream round trip after another: six that each take 300ms upstream arrive
//     in about the time of one, every byte right and in order; each still counts as a miss, none
//     as a hit, and the metrics show the prefetches

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
)

func TestPhortosServesAndStops(t *testing.T) {
	r := aptrepo.New(aptrepo.Options{Flat: true})
	defer r.Close()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	addr := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{
			Addr: "127.0.0.1:0", Dir: dir, Init: true, Hosts: []string{r.Host()}, Host: "test", PID: 1,
			Listening: func(a string) { addr <- a },
		}, slog.New(slog.DiscardHandler))
	}()
	var a string
	select {
	case a = <-addr:
	case err := <-done:
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + a + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	if err := server.Run(t.Context(), server.Options{Addr: "127.0.0.1:0", Dir: dir, Host: "other", PID: 2}, slog.New(slog.DiscardHandler)); errkind.Of(err) != errkind.Conflict {
		t.Errorf("a second phortos on the store: %v", err)
	}
	p, _ := url.Parse("http://" + a)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p)}}
	for _, path := range []string{r.InRelease(), r.Index(".gz"), r.Deb(0)} {
		resp, err := c.Get(r.URL() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Equal(body, r.File(path)) {
			t.Errorf("%s: %d, %d bytes", path, resp.StatusCode, len(body))
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("stopping: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("phortos did not stop")
	}
}

func TestNoStoreFolderRefusesToStart(t *testing.T) {
	if err := server.Run(t.Context(), server.Options{}, slog.New(slog.DiscardHandler)); errkind.Of(err) != errkind.Invalid {
		t.Errorf("got %v", err)
	}
}

func TestQueuedPackagesAreFetchedAtOnce(t *testing.T) {
	const n, slow = 6, 300 * time.Millisecond
	r := aptrepo.New(aptrepo.Options{Flat: true, Packages: n + 1})
	defer r.Close()
	p := runWith(t, server.Options{Dir: t.TempDir(), Hosts: []string{r.Host()}})
	defer p.stop()
	proxied := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: p.proxy})}}
	// A package asked for waits for the index being read, so after it phortos knows them all.
	for _, path := range []string{r.InRelease(), r.Index(".gz"), r.Asked(n)} {
		resp, err := proxied.Get(r.URL() + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	var queue strings.Builder
	for i := range n {
		r.Slow(r.Deb(i), slow)
		queue.WriteString("GET " + r.URL() + r.Asked(i) + " HTTP/1.1\r\nHost: " + r.Host() + "\r\n\r\n")
	}
	c, err := net.Dial("tcp", p.proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	start := time.Now()
	if _, err := io.WriteString(c, queue.String()); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	for i := range n {
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, r.File(r.Deb(i))) || resp.Header.Get("X-Phortos") != "miss" {
			t.Errorf("package %d: %d, %d bytes, from %s", i, resp.StatusCode, len(body), resp.Header.Get("X-Phortos"))
		}
	}
	if took := time.Since(start); took > 3*slow {
		t.Errorf("%d queued packages took %v; one after another they take %v", n, took, n*slow)
	}
	resp, err := http.Get("http://" + p.proxy + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	// The index, the package asked first and the queued ones were all downloaded now: misses.
	if hits, misses, prefetched := metric(t, metrics, `phortos_file_requests_total{result="hit"}`), metric(t, metrics, `phortos_file_requests_total{result="miss"}`), metric(t, metrics, "phortos_prefetched_files_total"); hits != 0 || misses != n+2 || prefetched < n {
		t.Errorf("hits %d, misses %d (want %d), prefetched %d (want at least %d)", hits, misses, n+2, prefetched, n)
	}
}

// metric is a series' value in Prometheus text.
func metric(t *testing.T, text []byte, series string) int {
	t.Helper()
	for line := range strings.SplitSeq(string(text), "\n") {
		if v, ok := strings.CutPrefix(line, series+" "); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatalf("no %s in\n%s", series, text)
	return 0
}
