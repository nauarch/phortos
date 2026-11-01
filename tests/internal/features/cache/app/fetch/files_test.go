// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch_test

// Verifies:
//   - A request that gives up does not stop the download: the file still lands for the next
//   - A download that stops sending is given up, fails its requests and keeps nothing; so is one
//     that crawls below the minimum rate for a whole window, while one at full speed lands
//   - Upstream announcing another size than the index fails at once and keeps nothing
//   - A file upstream does not have is not found
//   - A prefetch starts one download, once, and none for a stored file; the request it was for
//     joins it and counts as the miss it was, also once it landed, unless that was long ago; the
//     next request is a hit

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/features/cache/app/fetch"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
)

// rig is the fetchers over a real store, in front of a kit repository.
type rig struct {
	repo     *aptrepo.Repo
	store    *disk.Dir
	clock    *kitclock.Fixed
	counters *fetch.Counters
	files    *fetch.Files
	up       *upstream.HTTP
}

// newRig builds a rig whose downloads give up after stall without bytes.
func newRig(t *testing.T, stall time.Duration) *rig {
	t.Helper()
	r := aptrepo.New(aptrepo.Options{Flat: true})
	t.Cleanup(r.Close)
	store, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	up := upstream.New(upstream.Options{Timeout: 5 * time.Second})
	clk := kitclock.At(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	counters := &fetch.Counters{}
	return &rig{
		repo: r, store: store, clock: clk, counters: counters, up: up,
		files: &fetch.Files{Store: store, Upstream: up, Background: t.Context(), Clock: clk, StallAfter: stall, Counters: counters, Log: slog.New(slog.DiscardHandler)},
	}
}

// deb is package i's URL, digest and size.
func (rg *rig) deb(i int) (string, digest.Digest, int64) {
	b := rg.repo.File(rg.repo.Deb(i))
	return rg.repo.URL() + rg.repo.Deb(i), digest.Of(b), int64(len(b))
}

// stored reports whether a file is in the store.
func (rg *rig) stored(d digest.Digest) bool {
	f, _, err := rg.store.Blob(d)
	if err == nil {
		_ = f.Close()
	}
	return err == nil
}

func TestADownloadOutlivesTheRequestThatStartedIt(t *testing.T) {
	rg := newRig(t, 5*time.Second)
	url, d, size := rg.deb(0)
	rg.repo.Slow(rg.repo.Deb(0), 200*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	f, err := rg.files.Get(ctx, url, d, size)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = f.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !rg.stored(d) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil || got.Source != repo.Hit {
		t.Fatalf("after the first request left: %v %v", got.Source, err)
	}
	_ = got.Body.Close()
}

func TestAStalledDownloadIsGivenUpAndKeepsNothing(t *testing.T) {
	rg := newRig(t, 200*time.Millisecond)
	url, d, size := rg.deb(1)
	let := rg.repo.Hold(rg.repo.Deb(1))
	defer let()
	f, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(f.Body)
	_ = f.Body.Close()
	if errkind.Of(err) != errkind.Unreachable {
		t.Fatalf("a stalled download: %v", err)
	}
	if rg.stored(d) {
		t.Error("a stalled download was kept")
	}
}

func TestACrawlingDownloadIsGivenUpAndKeepsNothing(t *testing.T) {
	rg := newRig(t, 200*time.Millisecond)
	rg.files.MinRate = 10 << 10
	url, d, size := rg.deb(1)
	rg.repo.Trickle(rg.repo.Deb(1), 64, 20*time.Millisecond)
	f, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = io.ReadAll(f.Body)
	_ = f.Body.Close()
	if errkind.Of(err) != errkind.Unreachable || !strings.Contains(err.Error(), "below 10KiB a second") {
		t.Fatalf("a crawling download: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("given up after %s", took)
	}
	if rg.stored(d) || rg.counters.Failures.Load() != 1 {
		t.Error("a crawling download was kept or not counted")
	}
	url, d, size = rg.deb(2)
	g, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(g.Body); err != nil {
		t.Errorf("a download at full speed under the floor's watch: %v", err)
	}
	_ = g.Body.Close()
}

func TestAnotherSizeThanTheIndexFailsAtOnce(t *testing.T) {
	rg := newRig(t, 5*time.Second)
	url, d, size := rg.deb(2)
	if _, err := rg.files.Get(t.Context(), url, d, size+1); errkind.Of(err) != errkind.Upstream {
		t.Fatalf("got %v", err)
	}
	if rg.stored(d) || rg.counters.Failures.Load() != 1 {
		t.Error("a mismatched size was kept or not counted")
	}
}

// landed waits until a file is in the store.
func (rg *rig) landed(t *testing.T, d digest.Digest) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !rg.stored(d) {
		if time.Now().After(deadline) {
			t.Fatal("the file never landed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAPrefetchedFileCountsAsTheMissItWas(t *testing.T) {
	rg := newRig(t, 5*time.Second)
	url, d, size := rg.deb(0)
	let := rg.repo.Hold(rg.repo.Deb(0))
	if !rg.files.Prefetch(url, d, size) || rg.files.Prefetch(url, d, size) {
		t.Fatal("prefetch did not start once, and once only")
	}
	f, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil {
		t.Fatal(err)
	}
	let()
	body, _ := io.ReadAll(f.Body)
	_ = f.Body.Close()
	if f.Source != repo.Miss || string(body) != string(rg.repo.File(rg.repo.Deb(0))) {
		t.Errorf("joined a prefetch as %s, %d bytes", f.Source, len(body))
	}
	k := rg.counters
	if k.Prefetched.Load() != 1 || k.Misses.Load() != 1 || k.Joins.Load() != 0 || k.Hits.Load() != 0 || rg.repo.Hits(rg.repo.Deb(0)) != 1 {
		t.Errorf("prefetched %d, misses %d, joins %d, hits %d, upstream asked %d", k.Prefetched.Load(), k.Misses.Load(), k.Joins.Load(), k.Hits.Load(), rg.repo.Hits(rg.repo.Deb(0)))
	}
	if rg.files.Prefetch(url, d, size) {
		t.Error("a stored file was prefetched")
	}
	got, err := rg.files.Get(t.Context(), url, d, size)
	if err != nil || got.Source != repo.Hit {
		t.Fatalf("asked again: %v %v", got.Source, err)
	}
	_ = got.Body.Close()
}

func TestAPrefetchedFileAskedForAfterItLandedIsAMissUntilItIsOld(t *testing.T) {
	rg := newRig(t, 5*time.Second)
	for i, wait := range []time.Duration{0, 11 * time.Minute} {
		url, d, size := rg.deb(i)
		if !rg.files.Prefetch(url, d, size) {
			t.Fatal("not prefetched")
		}
		rg.landed(t, d)
		rg.clock.Advance(wait)
		got, err := rg.files.Get(t.Context(), url, d, size)
		if err != nil {
			t.Fatal(err)
		}
		_ = got.Body.Close()
		if want := map[time.Duration]repo.Source{0: repo.Miss, 11 * time.Minute: repo.Hit}[wait]; got.Source != want {
			t.Errorf("asked %v after it landed: %s, want %s", wait, got.Source, want)
		}
	}
}

func TestAFileUpstreamLacksIsNotFound(t *testing.T) {
	rg := newRig(t, 5*time.Second)
	if _, err := rg.files.Get(t.Context(), rg.repo.URL()+"/repo/x86_64/gone.deb", digest.Of([]byte("gone")), 4); errkind.Of(err) != errkind.NotFound {
		t.Fatalf("got %v", err)
	}
}
