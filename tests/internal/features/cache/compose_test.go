// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package cache_test

// Verifies, against an in-process repository in both shapes (classic with by-hash, and flat as
// NVIDIA's), with SHA256 sums or, as NVIDIA's container toolkit, SHA512 only:
//   - An apt run (release file, index, packages) is fully cached: the second run reaches upstream
//     for nothing but the release revalidation, and every byte matches
//   - Concurrent requests for one package share one upstream download; concurrent requests for a
//     release file share one upstream request
//   - A package whose bytes do not match its index is never kept nor served whole; an index not
//     matching the SHA512 its release lists is never kept nor served
//   - A file the catalog does not know, or asked for with a query, is passed through, never kept,
//     and logged with its URL and why
//   - A client that never asks for an index (its own is current) still has its packages kept:
//     phortos reads the index itself, once
//   - A new release has the indexes of the architectures read before read before any package is
//     asked (a client that patches its indexes with pdiffs never asks for them), so the new
//     packages are kept
//   - A release file is answered from the store while fresh, revalidated after, picks up a new
//     release, and is served stale when upstream is gone
//   - After a restart the kept releases are recalled, so packages are served from the store at
//     once
//   - Hosts off the list and malformed paths are refused
//   - Only a file whose sum is known is prefetched (not one no index read lists, not a release
//     file, a query, another host's, nor an index its release lists by SHA512 only); the request
//     for it then counts as a miss and upstream is asked once

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/features/cache"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
)

// rig is a cache over a real store, in front of a kit repository, on a fixed clock; it logs to
// log when set.
type rig struct {
	repo  *aptrepo.Repo
	store *disk.Dir
	clock *kitclock.Fixed
	cache *cache.Cache
	log   *slog.Logger
}

// newRig builds a rig; the repository closes when the test ends.
func newRig(t *testing.T, o aptrepo.Options) *rig {
	t.Helper()
	r := aptrepo.New(o)
	t.Cleanup(r.Close)
	store, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	rg := &rig{repo: r, store: store, clock: kitclock.At(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))}
	rg.cache = rg.compose(t)
	return rg
}

// compose builds a cache over the rig's store, as a (re)started phortos would.
func (rg *rig) compose(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.Compose(cache.Options{
		Store: rg.store, Upstream: upstream.New(upstream.Options{Timeout: 5 * time.Second}),
		Hosts: []string{rg.repo.Host()}, StallAfter: 2 * time.Second, IndexWait: 5 * time.Second,
		Background: t.Context(), Clock: rg.clock, Log: cmp.Or(rg.log, slog.New(slog.DiscardHandler)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// get fetches a path through the cache and reads it whole.
func (rg *rig) get(t *testing.T, p string) (repo.File, []byte) {
	t.Helper()
	f, err := rg.cache.Get(t.Context(), rg.repo.URL()+p, repo.Ask{})
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	defer f.Body.Close()
	body, err := io.ReadAll(f.Body)
	if err != nil {
		t.Fatalf("%s: reading: %v", p, err)
	}
	return f, body
}

// aptRun asks for what apt asks for: the release file, the index (by hash when served so) and
// every package, by the path apt asks for ("./name" in a flat repository); each must match the
// repository's bytes. Sources are keyed by the clean path.
func (rg *rig) aptRun(t *testing.T, byHash bool, ext string) map[string]repo.Source {
	t.Helper()
	sources := map[string]repo.Source{}
	index := rg.repo.Index(ext)
	type ask struct{ path, asked string }
	asks := []ask{{rg.repo.InRelease(), rg.repo.InRelease()}, {index, index}}
	if byHash {
		asks[1].asked = rg.repo.ByHashPath(index)
	}
	for i := range 3 {
		asks = append(asks, ask{rg.repo.Deb(i), rg.repo.Asked(i)})
	}
	for _, a := range asks {
		p := a.path
		f, body := rg.get(t, a.asked)
		want := rg.repo.File(p)
		if f.Status != http.StatusOK || !bytes.Equal(body, want) {
			t.Fatalf("%s: status %d, %d bytes, want %d", p, f.Status, len(body), len(want))
		}
		sources[p] = f.Source
	}
	return sources
}

func TestOnlyAKnownFileIsPrefetched(t *testing.T) {
	rg := newRig(t, aptrepo.Options{Flat: true})
	deb := rg.repo.URL() + rg.repo.Asked(0)
	if rg.cache.Prefetch(deb) || rg.repo.Hits(rg.repo.Deb(0)) != 0 {
		t.Fatal("a package no index read lists was prefetched")
	}
	rg.get(t, rg.repo.InRelease())
	rg.get(t, rg.repo.Index(".gz"))
	for _, u := range []string{
		rg.repo.URL() + rg.repo.InRelease(),
		deb + "?x=1",
		"http://elsewhere.example" + rg.repo.Asked(0),
		rg.repo.URL() + "/repo/x86_64/unlisted.deb",
	} {
		if rg.cache.Prefetch(u) {
			t.Errorf("prefetched %s", u)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for !rg.cache.Prefetch(deb) {
		if time.Now().After(deadline) {
			t.Fatal("a listed package was not prefetched")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f, body := rg.get(t, rg.repo.Asked(0))
	if f.Source != repo.Miss || !bytes.Equal(body, rg.repo.File(rg.repo.Deb(0))) || rg.repo.Hits(rg.repo.Deb(0)) != 1 {
		t.Errorf("after a prefetch: %s, %d bytes, upstream asked %d times", f.Source, len(body), rg.repo.Hits(rg.repo.Deb(0)))
	}
	if s := rg.cache.Stats(); s.Prefetched != 1 || s.Misses != 2 || s.Hits != 0 {
		t.Errorf("the index and the package are the misses, the prefetch no request: %+v", s)
	}
	strong := newRig(t, aptrepo.Options{Flat: true, SHA512: true})
	strong.get(t, strong.repo.InRelease())
	if strong.cache.Prefetch(strong.repo.URL() + strong.repo.Index(".gz")) {
		t.Error("an index its release lists by SHA512 only was prefetched")
	}
	plain := newRig(t, aptrepo.Options{Flat: true})
	plain.get(t, plain.repo.InRelease())
	if !plain.cache.Prefetch(plain.repo.URL() + plain.repo.Index(".gz")) {
		t.Error("an index its release lists by SHA256 was not prefetched")
	}
}

func TestAnAptRunIsFullyCachedInBothShapes(t *testing.T) {
	for name, o := range map[string]struct {
		opts   aptrepo.Options
		byHash bool
		ext    string
	}{
		"classic, by hash, xz index":       {aptrepo.Options{ByHash: true}, true, ".xz"},
		"flat, gzip index":                 {aptrepo.Options{Flat: true}, false, ".gz"},
		"flat, SHA512 only, gzip index":    {aptrepo.Options{Flat: true, SHA512: true}, false, ".gz"},
		"classic, SHA512 only, gzip index": {aptrepo.Options{SHA512: true}, false, ".gz"},
	} {
		t.Run(name, func(t *testing.T) {
			rg := newRig(t, o.opts)
			first := rg.aptRun(t, o.byHash, o.ext)
			if first[rg.repo.Deb(0)] != repo.Miss {
				t.Errorf("first run: package came from %s", first[rg.repo.Deb(0)])
			}
			rg.clock.Advance(2 * time.Minute)
			second := rg.aptRun(t, o.byHash, o.ext)
			for p, s := range second {
				if p != rg.repo.InRelease() && s != repo.Hit {
					t.Errorf("second run: %s came from %s", p, s)
				}
			}
			if second[rg.repo.InRelease()] != repo.Checked {
				t.Errorf("release: %s, want checked", second[rg.repo.InRelease()])
			}
			for i := range 3 {
				if n := rg.repo.Hits(rg.repo.Deb(i)); n != 1 {
					t.Errorf("package %d reached upstream %d times", i, n)
				}
			}
		})
	}
}

func TestConcurrentRequestsShareOneUpstreamRequest(t *testing.T) {
	rg := newRig(t, aptrepo.Options{ByHash: true})
	rg.aptRun(t, true, ".xz")
	rg.repo.Publish()
	rg.clock.Advance(2 * time.Minute)
	let := rg.repo.Hold(rg.repo.InRelease())
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() { rg.get(t, rg.repo.InRelease()) })
	}
	time.Sleep(200 * time.Millisecond)
	let()
	wg.Wait()
	if n := rg.repo.Hits(rg.repo.InRelease()); n != 2 {
		t.Errorf("release reached upstream %d times, want 2 (first run, then once for six hosts)", n)
	}
	rg.get(t, rg.repo.ByHashPath(rg.repo.Index(".xz")))
	deb := rg.repo.Deb(1)
	let = rg.repo.Hold(deb)
	results := make([][]byte, 6)
	for i := range 6 {
		wg.Go(func() { _, results[i] = rg.get(t, deb) })
	}
	time.Sleep(200 * time.Millisecond)
	let()
	wg.Wait()
	for i, b := range results {
		if !bytes.Equal(b, rg.repo.File(deb)) {
			t.Errorf("request %d got %d bytes", i, len(b))
		}
	}
	if n := rg.repo.Hits(deb); n != 2 {
		t.Errorf("package reached upstream %d times, want 2 (first run, then once for six hosts)", n)
	}
}

func TestACorruptPackageIsNeverKept(t *testing.T) {
	rg := newRig(t, aptrepo.Options{Flat: true})
	rg.get(t, rg.repo.InRelease())
	rg.get(t, rg.repo.Index(".gz"))
	deb := rg.repo.Deb(2)
	rg.repo.Corrupt(deb)
	f, err := rg.cache.Get(t.Context(), rg.repo.URL()+deb, repo.Ask{})
	if err == nil {
		_, err = io.ReadAll(f.Body)
		_ = f.Body.Close()
	}
	if errkind.Of(err) != errkind.Upstream {
		t.Fatalf("a corrupt package: %v", err)
	}
	if entries, _ := rg.store.Blobs(); len(entries) != 2 {
		t.Errorf("store holds %d files, want 2 (InRelease and Packages.gz, not the corrupt package)", len(entries))
	}
}

func TestAnIndexNotMatchingItsSHA512IsNeverKept(t *testing.T) {
	rg := newRig(t, aptrepo.Options{Flat: true, SHA512: true})
	rg.get(t, rg.repo.InRelease())
	rg.repo.Corrupt(rg.repo.Index(".gz"))
	if f, err := rg.cache.Get(t.Context(), rg.repo.URL()+rg.repo.Index(".gz"), repo.Ask{}); err == nil {
		_ = f.Body.Close()
		t.Fatalf("a corrupt index was served from %s", f.Source)
	}
	if entries, _ := rg.store.Blobs(); len(entries) != 1 {
		t.Errorf("store holds %d files, want 1 (the InRelease only)", len(entries))
	}
}

// lockedBuffer is a log that background work may write to while a test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestAnUnknownFileIsPassedThrough(t *testing.T) {
	rg := newRig(t, aptrepo.Options{Flat: true})
	logged := &lockedBuffer{}
	rg.log = slog.New(slog.NewTextHandler(logged, nil))
	rg.cache = rg.compose(t)
	f, body := rg.get(t, rg.repo.Deb(0))
	if f.Source != repo.Passed || !bytes.Equal(body, rg.repo.File(rg.repo.Deb(0))) {
		t.Errorf("source %s", f.Source)
	}
	rg.get(t, rg.repo.Deb(1)+"?x=1")
	for _, want := range []string{
		`msg="passed through" url=` + rg.repo.URL() + rg.repo.Deb(0) + ` why="no index read lists it" status=200`,
		`msg="passed through" url="` + rg.repo.URL() + rg.repo.Deb(1) + `?x=1" why="a query" status=200`,
	} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("not logged: %s\n%s", want, logged.String())
		}
	}
	if entries, _ := rg.store.Blobs(); len(entries) != 0 {
		t.Errorf("an unverified file was kept: %v", entries)
	}
	f, _ = rg.get(t, "/no/such/file")
	if f.Status != http.StatusNotFound {
		t.Errorf("missing file: %d", f.Status)
	}
}

func TestAClientWithItsIndexesAlreadyHasItsPackagesCached(t *testing.T) {
	for name, o := range map[string]aptrepo.Options{"classic, by hash": {ByHash: true}, "flat": {Flat: true}, "flat, SHA512 only": {Flat: true, SHA512: true}} {
		t.Run(name, func(t *testing.T) {
			rg := newRig(t, o)
			rg.get(t, rg.repo.InRelease())
			for i := range 3 {
				f, body := rg.get(t, rg.repo.Asked(i))
				if f.Source != repo.Miss || !bytes.Equal(body, rg.repo.File(rg.repo.Deb(i))) {
					t.Errorf("package %d came from %s", i, f.Source)
				}
			}
			if f, _ := rg.get(t, rg.repo.Asked(0)); f.Source != repo.Hit {
				t.Errorf("asked again: %s", f.Source)
			}
			if n := rg.repo.Hits(rg.repo.Index(".gz")); n != 1 {
				t.Errorf("the index reached upstream %d times", n)
			}
		})
	}
}

func TestANewReleaseHasItsIndexesReadBeforeAnyPackageIsAsked(t *testing.T) {
	rg := newRig(t, aptrepo.Options{ByHash: true})
	rg.get(t, rg.repo.InRelease())
	rg.get(t, rg.repo.Asked(0))
	rg.repo.Publish()
	rg.clock.Advance(2 * time.Minute)
	if f, _ := rg.get(t, rg.repo.InRelease()); f.Source != repo.Miss {
		t.Fatalf("new release: %s", f.Source)
	}
	index := rg.repo.Index(".gz")
	for deadline := time.Now().Add(5 * time.Second); rg.repo.Hits(index) < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := rg.repo.Hits(index); n != 2 {
		t.Fatalf("the new index reached upstream %d times, want 2", n)
	}
	f, body := rg.get(t, rg.repo.Asked(0))
	if f.Source != repo.Miss || !bytes.Equal(body, rg.repo.File(rg.repo.Deb(0))) {
		t.Errorf("the new package came from %s", f.Source)
	}
	if n := rg.repo.Hits(index); n != 2 {
		t.Errorf("the index reached upstream %d times", n)
	}
}

func TestReleasesAreFreshThenRevalidatedThenStale(t *testing.T) {
	rg := newRig(t, aptrepo.Options{ByHash: true})
	in := rg.repo.InRelease()
	if f, _ := rg.get(t, in); f.Source != repo.Miss {
		t.Errorf("first: %s", f.Source)
	}
	if f, _ := rg.get(t, in); f.Source != repo.Hit {
		t.Errorf("within the TTL: %s", f.Source)
	}
	rg.clock.Advance(2 * time.Minute)
	if f, _ := rg.get(t, in); f.Source != repo.Checked {
		t.Errorf("after the TTL: %s", f.Source)
	}
	rg.repo.Publish()
	rg.clock.Advance(2 * time.Minute)
	if f, body := rg.get(t, in); f.Source != repo.Miss || !bytes.Equal(body, rg.repo.File(in)) {
		t.Errorf("new release: %s", f.Source)
	}
	newest := rg.repo.File(in)
	rg.repo.Close()
	rg.clock.Advance(2 * time.Minute)
	if f, body := rg.get(t, in); f.Source != repo.Stale || !bytes.Equal(body, newest) {
		t.Errorf("upstream gone: %s", f.Source)
	}
}

func TestKeptReleasesAreRecalledAfterARestart(t *testing.T) {
	rg := newRig(t, aptrepo.Options{Flat: true})
	rg.aptRun(t, false, ".gz")
	restarted := rg.compose(t)
	if err := restarted.Recall(); err != nil {
		t.Fatal(err)
	}
	rg.cache = restarted
	if f, _ := rg.get(t, rg.repo.Index(".gz")); f.Source != repo.Hit {
		t.Errorf("index after a restart: %s", f.Source)
	}
	if f, _ := rg.get(t, rg.repo.Deb(0)); f.Source != repo.Hit {
		t.Errorf("package after a restart and its index: %s", f.Source)
	}
}

func TestHostsOffTheListAndBadPathsAreRefused(t *testing.T) {
	rg := newRig(t, aptrepo.Options{})
	ctx := context.Background()
	if _, err := rg.cache.Get(ctx, "http://evil.example/debian/dists/x/InRelease", repo.Ask{}); errkind.Of(err) != errkind.Forbidden {
		t.Errorf("off the list: %v", err)
	}
	for _, u := range []string{rg.repo.URL() + "/a/../../etc/passwd", "ftp://" + rg.repo.Host() + "/x", "http://user@" + rg.repo.Host() + "/x"} {
		if _, err := rg.cache.Get(ctx, u, repo.Ask{}); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%s: %v", u, err)
		}
	}
	if !rg.cache.Serves(rg.repo.Host()) || rg.cache.Serves("evil.example") {
		t.Error("Serves is wrong")
	}
}
