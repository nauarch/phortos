// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package cache is phortos' caching proxy for apt repositories: release files kept and
// revalidated, index and package files kept only once verified against the SHA256 their
// repository lists, everything else passed through.
package cache

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/app/evict"
	"github.com/nauarch/phortos/internal/features/cache/app/fetch"
	"github.com/nauarch/phortos/internal/features/cache/domain/allow"
	"github.com/nauarch/phortos/internal/features/cache/domain/layout"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// The defaults of the cache's timing.
const (
	// DefaultReleaseTTL is how long a fetched release file is answered without asking upstream:
	// long enough that a fleet updating at once asks once, short enough that a new release is
	// seen within a minute.
	DefaultReleaseTTL = time.Minute
	// DefaultStallAfter is how long a download may receive nothing before it is given up.
	DefaultStallAfter = time.Minute
	// DefaultMinRate is the bytes a second below which, over a whole stall window, a download is
	// given up: far under any working link, far over a connection gone bad (a degraded one to
	// Launchpad was seen delivering a few KB/s while a fresh one took 12 MB/s).
	DefaultMinRate = 32 << 10
	// DefaultIndexWait is how long a request for an unknown file waits for a Packages index being
	// read that may list it, before it is passed through.
	DefaultIndexWait = 30 * time.Second
	// DefaultReleaseWithin is how long a release file's revalidation may take before the kept
	// copy is served stale: well under apt's own timeout, so a slow repository never stalls an
	// update phortos could have answered.
	DefaultReleaseWithin = 10 * time.Second
	// DefaultEvictEvery is how often the store is measured and, over its cap, trimmed.
	DefaultEvictEvery = 5 * time.Minute
)

// Options are what the cache is built from: where it keeps things, where it fetches them from,
// which hosts it serves (patterns such as deb.debian.org or *.archive.ubuntu.com), its timing
// (zero takes the defaults; a negative ReleaseWithin waits as long as upstream takes), the
// bytes a second below which a download is given up (MinRate, 0 for no floor), the store's cap in
// bytes (0 for none), the context its downloads run on (they stop when it ends),
// the clock and the log.
type Options struct {
	Store         storage.Store
	Upstream      upstream.Upstream
	Hosts         []string
	ReleaseTTL    time.Duration
	ReleaseWithin time.Duration
	StallAfter    time.Duration
	MinRate       int64
	IndexWait     time.Duration
	MaxBytes      int64
	EvictEvery    time.Duration
	Background    context.Context
	Clock         clock.Clock
	Log           *slog.Logger
}

// Cache is the caching proxy: a repo.Fetcher for the proxy door and an ops.Reporter for the ops
// door.
type Cache struct {
	store      storage.Store
	hosts      atomic.Pointer[allow.List]
	catalog    *catalog.Catalog
	counters   *fetch.Counters
	files      *fetch.Files
	strong     *fetch.Strong
	releases   *fetch.Releases
	indexes    *fetch.Indexes
	pass       *fetch.Pass
	evictor    *evict.Evictor
	indexWait  time.Duration
	evictEvery time.Duration
	log        *slog.Logger
}

var (
	_ repo.Fetcher = (*Cache)(nil)
	_ ops.Reporter = (*Cache)(nil)
)

// Compose builds the cache. A store, an upstream and at least one host are required.
func Compose(o Options) (*Cache, error) {
	if o.Store == nil || o.Upstream == nil {
		return nil, fmt.Errorf("cache: a store and an upstream are required: %w", errkind.ErrInvalid)
	}
	hosts, err := hostList(o.Hosts)
	if err != nil {
		return nil, err
	}
	o = withDefaults(o)
	counters := &fetch.Counters{}
	cat := catalog.New()
	files := &fetch.Files{Store: o.Store, Upstream: o.Upstream, Background: o.Background, Clock: o.Clock, StallAfter: o.StallAfter, MinRate: o.MinRate, Counters: counters, Log: o.Log}
	strong := &fetch.Strong{Store: o.Store, Upstream: o.Upstream, Background: o.Background, Counters: counters, Log: o.Log}
	indexes := &fetch.Indexes{Files: files, Strong: strong, Catalog: cat, Background: o.Background, Counters: counters, Log: o.Log}
	evictor := &evict.Evictor{Store: o.Store, Log: o.Log}
	evictor.Max.Store(o.MaxBytes)
	c := &Cache{
		store:    o.Store,
		catalog:  cat,
		counters: counters,
		files:    files,
		strong:   strong,
		releases: &fetch.Releases{
			Store: o.Store, Upstream: o.Upstream, Catalog: cat, Read: indexes.Read, Background: o.Background, Clock: o.Clock,
			TTL: o.ReleaseTTL, Within: o.ReleaseWithin, Counters: counters, Log: o.Log,
		},
		indexes:    indexes,
		pass:       &fetch.Pass{Upstream: o.Upstream, Counters: counters},
		evictor:    evictor,
		indexWait:  o.IndexWait,
		evictEvery: o.EvictEvery,
		log:        o.Log,
	}
	c.hosts.Store(&hosts)
	return c, nil
}

// SetHosts changes the hosts served while phortos runs; a list that is not valid or empty
// changes nothing.
func (c *Cache) SetHosts(patterns []string) error {
	hosts, err := hostList(patterns)
	if err != nil {
		return err
	}
	c.hosts.Store(&hosts)
	return nil
}

// SetMax changes the store's cap (0 for none) while phortos runs; the next eviction pass keeps it.
func (c *Cache) SetMax(bytes int64) error {
	if bytes < 0 {
		return fmt.Errorf("cache: a negative store cap: %w", errkind.ErrInvalid)
	}
	c.evictor.Max.Store(bytes)
	return nil
}

// hostList is the allow list of host patterns, refusing an empty one.
func hostList(patterns []string) (allow.List, error) {
	hosts, err := allow.New(patterns)
	if err != nil {
		return allow.List{}, fmt.Errorf("cache: %w", err)
	}
	if len(hosts.Patterns()) == 0 {
		return allow.List{}, fmt.Errorf("cache: no repository hosts to serve: %w", errkind.ErrInvalid)
	}
	return hosts, nil
}

// Maintain keeps the store under its cap, measuring it now and every EvictEvery, until ctx ends.
func (c *Cache) Maintain(ctx context.Context) { c.evictor.Run(ctx, c.evictEvery) }

// Healthy fails when the store cannot be written to.
func (c *Cache) Healthy() error { return c.store.Check() }

// Stats are the cache's counters and the store's size.
func (c *Cache) Stats() ops.Stats {
	k, e := c.counters, c.evictor
	return ops.Stats{
		Hits: k.Hits.Load(), Joins: k.Joins.Load(), Misses: k.Misses.Load(), Failures: k.Failures.Load(),
		ReleasesFresh: k.ReleasesFresh.Load(), ReleasesChecked: k.ReleasesChecked.Load(),
		ReleasesChanged: k.ReleasesChanged.Load(), ReleasesStale: k.ReleasesStale.Load(),
		Passed: k.Passed.Load(), Prefetched: k.Prefetched.Load(), PackagesRead: k.PackagesRead.Load(), PackagesReadFailed: k.PackagesReadFailed.Load(),
		BytesServed: k.BytesServed.Load(), BytesFetched: k.BytesFetched.Load(), BytesPassed: k.BytesPassed.Load(),
		Evicted: e.Evicted.Load(), EvictedBytes: e.EvictedBytes.Load(),
		StoreBytes: e.StoreBytes.Load(), StoreFiles: e.StoreBlobs.Load(), MaxBytes: e.Max.Load(),
	}
}

// withDefaults fills what the options leave empty.
func withDefaults(o Options) Options {
	if o.Background == nil {
		o.Background = context.Background()
	}
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.ReleaseTTL == 0 {
		o.ReleaseTTL = DefaultReleaseTTL
	}
	if o.StallAfter <= 0 {
		o.StallAfter = DefaultStallAfter
	}
	if o.IndexWait <= 0 {
		o.IndexWait = DefaultIndexWait
	}
	if o.ReleaseWithin == 0 {
		o.ReleaseWithin = DefaultReleaseWithin
	}
	if o.EvictEvery <= 0 {
		o.EvictEvery = DefaultEvictEvery
	}
	return o
}

// Recall teaches the catalog every release file kept from before, so files are verified (and
// served from the store) right after a restart.
func (c *Cache) Recall() error {
	recs, err := c.store.Releases()
	if err != nil {
		return err
	}
	for _, r := range recs {
		c.releases.Learn(r)
	}
	c.log.Info("kept releases recalled", "releases", len(recs))
	return nil
}

// Serves reports whether a host is on the cache's list.
func (c *Cache) Serves(host string) bool { return c.hosts.Load().Allows(host) }

// Hosts are the host patterns the cache serves.
func (c *Cache) Hosts() []string { return c.hosts.Load().Patterns() }

// Get is the file at raw, a URL as the client asked for it. A package file waits for any
// Packages index of its repository still being read, so a package is always checked against
// the newest index phortos has served, never an older one listing other bytes under the same
// name. A package file no index read so far lists has the indexes that can list it read first
// (see catalog.Unread), and is passed through only when they do not list it either.
func (c *Cache) Get(ctx context.Context, raw string, ask repo.Ask) (repo.File, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return repo.File{}, fmt.Errorf("url %q: %w", raw, errkind.ErrInvalid)
	}
	host := strings.ToLower(u.Host)
	if !c.Serves(host) {
		return repo.File{}, fmt.Errorf("host %s is not served: %w", host, errkind.ErrForbidden)
	}
	u.Path, u.RawPath = layout.Tidy(u.Path), layout.Tidy(u.RawPath)
	if !layout.ValidPath(u.Path) {
		return repo.File{}, fmt.Errorf("path %q: %w", u.Path, errkind.ErrInvalid)
	}
	key := u.Scheme + "://" + host + u.Path
	fetchURL := u.Scheme + "://" + host + u.EscapedPath()
	if u.RawQuery != "" {
		return c.passThrough(ctx, fetchURL+"?"+u.RawQuery, "a query", ask)
	}
	if layout.IsRelease(u.Path) {
		return c.releases.Get(ctx, key)
	}
	if hex, ok := layout.ByHash(u.Path); ok {
		return c.known(ctx, key, fetchURL, release.Sum{Hex: hex, Size: c.catalog.SizeOf(hex)})
	}
	if sum, ok := c.catalog.Index(key); ok {
		return c.known(ctx, key, fetchURL, sum)
	}
	if sum, ok := c.lookup(ctx, key); ok {
		return c.known(ctx, key, fetchURL, sum)
	}
	if loads := c.catalog.Unread(key); len(loads) > 0 {
		c.log.Info("reading packages indexes for an unknown package", "url", key, "indexes", len(loads))
		for _, l := range loads {
			c.indexes.Read(l)
		}
		if sum, ok := c.lookup(ctx, key); ok {
			return c.known(ctx, key, fetchURL, sum)
		}
	}
	return c.passThrough(ctx, fetchURL, "no index read lists it", ask)
}

// passThrough passes a file phortos cannot check through uncached, and logs it with why: what a
// client got from upstream unchecked and not kept is worth knowing, and rare.
func (c *Cache) passThrough(ctx context.Context, url, why string, ask repo.Ask) (repo.File, error) {
	f, err := c.pass.Get(ctx, url, ask)
	if err != nil {
		return f, err
	}
	c.log.Info("passed through", "url", url, "why", why, "status", f.Status, "size", f.Size)
	return f, nil
}

// Prefetch starts downloading the file at raw, a URL as a client asked for it, ahead of that
// request: a client queued it behind others on one connection, and phortos answers those in order.
// Only a file whose sum phortos knows already is fetched; it never passes a file through, reads
// no index and waits for none, so whatever it cannot start at once is left to the request itself.
// It says whether it started a download.
func (c *Cache) Prefetch(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" {
		return false
	}
	host := strings.ToLower(u.Host)
	if !c.Serves(host) {
		return false
	}
	u.Path, u.RawPath = layout.Tidy(u.Path), layout.Tidy(u.RawPath)
	if !layout.ValidPath(u.Path) || layout.IsRelease(u.Path) {
		return false
	}
	key := u.Scheme + "://" + host + u.Path
	sum, ok := release.Sum{}, false
	if hex, byHash := layout.ByHash(u.Path); byHash {
		sum, ok = release.Sum{Hex: hex, Size: c.catalog.SizeOf(hex)}, true
	} else if sum, ok = c.catalog.Index(key); !ok {
		sum, ok = c.catalog.Lookup(key)
	}
	if !ok || sum.SHA512 {
		return false
	}
	d, err := digest.Parse(digest.Algorithm + ":" + sum.Hex)
	if err != nil {
		return false
	}
	return c.files.Prefetch(u.Scheme+"://"+host+u.EscapedPath(), d, sum.Size)
}

// lookup is the sum of the file at key once no index that may list it is being read, waiting at
// most indexWait for them.
func (c *Cache) lookup(ctx context.Context, key string) (release.Sum, bool) {
	wait, cancel := context.WithTimeout(ctx, c.indexWait)
	c.catalog.Wait(wait, key)
	cancel()
	return c.catalog.Lookup(key)
}

// known is a file whose sum is known: kept, and when it is a Packages index, read so the
// packages it lists become known too. A file known by its SHA512 only is checked and kept first,
// so it is served from the store by its SHA256 (see fetch.Strong).
func (c *Cache) known(ctx context.Context, key, fetchURL string, sum release.Sum) (repo.File, error) {
	if l, ok := c.catalog.Claim(sum.Hex, key); ok {
		c.indexes.Read(l)
	}
	d, err := c.strong.Digest(ctx, fetchURL, sum)
	if err != nil {
		return repo.File{}, err
	}
	return c.files.Get(ctx, fetchURL, d, sum.Size)
}
