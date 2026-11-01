// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch_test

// Verifies:
//   - A release file upstream does not have passes its status through and keeps nothing
//   - A release file upstream fails to serve (5xx) is answered stale when kept, and fails when not
//   - A release file is learned by the catalog, so its index becomes known
//   - A revalidation slower than Within is answered stale at once, and its late answer is kept

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/app/fetch"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
)

const releaseFile = "Origin: test\nSHA256:\n aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 7 Packages.gz\n"

// releasesRig is a Releases fetcher in front of a server whose status the test sets.
func releasesRig(t *testing.T, status *atomic.Int64) (*fetch.Releases, *catalog.Catalog, *kitclock.Fixed, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := int(status.Load()); s != http.StatusOK {
			w.WriteHeader(s)
			return
		}
		_, _ = w.Write([]byte(releaseFile))
	}))
	t.Cleanup(srv.Close)
	store, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New()
	clk := kitclock.At(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	r := &fetch.Releases{
		Store: store, Upstream: upstream.New(upstream.Options{}), Catalog: cat, Background: t.Context(),
		Clock: clk, TTL: time.Minute, Counters: &fetch.Counters{}, Log: slog.New(slog.DiscardHandler),
	}
	return r, cat, clk, srv.URL + "/repo/InRelease"
}

func TestAMissingReleasePassesItsStatus(t *testing.T) {
	var status atomic.Int64
	status.Store(http.StatusNotFound)
	r, _, _, url := releasesRig(t, &status)
	f, err := r.Get(t.Context(), url)
	if err != nil || f.Status != http.StatusNotFound || f.Source != repo.Passed {
		t.Fatalf("%+v %v", f, err)
	}
}

func TestAFailingUpstreamIsAnsweredStale(t *testing.T) {
	var status atomic.Int64
	status.Store(http.StatusServiceUnavailable)
	r, cat, clk, url := releasesRig(t, &status)
	if _, err := r.Get(t.Context(), url); errkind.Of(err) != errkind.Upstream {
		t.Fatalf("nothing kept: %v", err)
	}
	status.Store(http.StatusOK)
	if f, err := r.Get(t.Context(), url); err != nil || f.Source != repo.Miss {
		t.Fatalf("%v %v", f.Source, err)
	}
	if _, ok := cat.Lookup(url[:len(url)-len("InRelease")] + "Packages.gz"); !ok {
		t.Error("the catalog did not learn the release")
	}
	status.Store(http.StatusBadGateway)
	clk.Advance(2 * time.Minute)
	if f, err := r.Get(t.Context(), url); err != nil || f.Source != repo.Stale {
		t.Fatalf("%v %v", f.Source, err)
	}
}

func TestASlowUpstreamIsAnsweredStaleAndStillKept(t *testing.T) {
	var hold atomic.Bool
	gate := make(chan struct{})
	body := atomic.Value{}
	body.Store(releaseFile)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold.Load() {
			<-gate
		}
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(gate) })
	store, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	clk := kitclock.At(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	r := &fetch.Releases{
		Store: store, Upstream: upstream.New(upstream.Options{}), Catalog: catalog.New(), Background: t.Context(),
		Clock: clk, TTL: time.Minute, Within: 100 * time.Millisecond, Counters: &fetch.Counters{}, Log: slog.New(slog.DiscardHandler),
	}
	url := srv.URL + "/repo/InRelease"
	if f, err := r.Get(t.Context(), url); err != nil || f.Source != repo.Miss {
		t.Fatalf("first: %v %v", f.Source, err)
	}
	first, _ := store.Release(url)
	hold.Store(true)
	body.Store(releaseFile + "Date: later\n")
	clk.Advance(2 * time.Minute)
	start := time.Now()
	f, err := r.Get(t.Context(), url)
	if err != nil || f.Source != repo.Stale || time.Since(start) > 2*time.Second {
		t.Fatalf("slow: %v %v after %s", f.Source, err, time.Since(start))
	}
	_ = f.Body.Close()
	gate <- struct{}{}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if rec, err := store.Release(url); err == nil && rec.Digest != first.Digest {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the late answer was not kept")
		}
	}
	if f, err := r.Get(t.Context(), url); err != nil || f.Source != repo.Hit {
		t.Errorf("after the late answer: %v %v", f.Source, err)
	}
}
