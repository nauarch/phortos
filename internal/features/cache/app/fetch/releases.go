// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// maxRelease bounds a release file's size; Debian's largest are a few hundred kilobytes.
const maxRelease = 64 << 20

// Releases hands out release files (InRelease, Release, Release.gpg). They change in place, so
// one is answered from the store for TTL after it was fetched, then asked upstream again
// (conditionally, so usually a 304), and kept and served stale when upstream cannot be asked or
// does not answer within Within (zero waits as long as upstream takes); a fetch that missed the
// deadline still finishes and keeps what it got. Every release fetched teaches the catalog its
// index sums, and Read (required once the catalog has read an index) reads the indexes the
// catalog wants read at once (those of a release that changed, see catalog.Learn). Concurrent requests for one URL share a single upstream request,
// so a fleet updating at once asks upstream once.
type Releases struct {
	Store      storage.Store
	Upstream   upstream.Upstream
	Catalog    *catalog.Catalog
	Read       func(catalog.Load)
	Background context.Context
	Clock      clock.Clock
	TTL        time.Duration
	Within     time.Duration
	Counters   *Counters
	Log        *slog.Logger

	mu    sync.Mutex
	calls map[string]*call
}

// call is one round of answering a release URL, shared by every request that arrives during it.
// A late call is a fetch finishing after its requests were answered stale: it only keeps.
type call struct {
	done   chan struct{}
	body   []byte
	rec    storage.ReleaseRecord
	status int
	source repo.Source
	err    error
	late   bool
}

// fetched is what upstream answered.
type fetched struct {
	resp upstream.Response
	err  error
}

// Get is the release file at url.
func (r *Releases) Get(ctx context.Context, url string) (repo.File, error) {
	r.mu.Lock()
	c, joined := r.calls[url]
	if !joined {
		if r.calls == nil {
			r.calls = map[string]*call{}
		}
		c = &call{done: make(chan struct{})}
		r.calls[url] = c
		go func() {
			r.answer(c, url)
			r.mu.Lock()
			delete(r.calls, url)
			r.mu.Unlock()
			close(c.done)
		}()
	}
	r.mu.Unlock()
	select {
	case <-c.done:
	case <-ctx.Done():
		return repo.File{}, ctx.Err()
	}
	if c.err != nil {
		return repo.File{}, c.err
	}
	if c.status != http.StatusOK {
		return repo.File{Status: c.status, Size: 0, Body: io.NopCloser(bytes.NewReader(nil)), Source: repo.Passed}, nil
	}
	r.Counters.BytesServed.Add(int64(len(c.body)))
	return repo.File{
		Status: http.StatusOK, Size: int64(len(c.body)), Modified: c.rec.Modified, ETag: c.rec.ETag,
		Body: readSeekNopCloser{bytes.NewReader(c.body)}, Source: c.source,
	}, nil
}

// answer fills a call: from the store while fresh, else from upstream, else stale. The kept copy
// is read first, so a conditional fetch is only made for a copy that is really there.
func (r *Releases) answer(c *call, url string) {
	var kept []byte
	rec, err := r.Store.Release(url)
	if err == nil {
		kept, err = r.stored(url, rec)
	}
	have := err == nil
	if have && r.Clock.Now().Sub(rec.Fetched) < r.TTL {
		c.body, c.rec, c.status, c.source = kept, rec, http.StatusOK, repo.Hit
		r.Counters.ReleasesFresh.Add(1)
		return
	}
	var cond upstream.Conditions
	if have {
		cond = upstream.Conditions{Modified: rec.Modified, ETag: rec.ETag}
	}
	got := make(chan fetched, 1)
	go func() {
		resp, err := r.Upstream.Fetch(r.Background, url, cond)
		got <- fetched{resp, err}
	}()
	if !have || r.Within <= 0 {
		f := <-got
		r.settle(c, url, rec, kept, have, f.resp, f.err)
		return
	}
	deadline := time.NewTimer(r.Within)
	defer deadline.Stop()
	select {
	case f := <-got:
		r.settle(c, url, rec, kept, have, f.resp, f.err)
	case <-deadline.C:
		r.stale(c, url, rec, kept, have, fmt.Errorf("%s: no answer within %s: %w", url, r.Within, errkind.ErrUnreachable))
		go func() {
			f := <-got
			r.settle(&call{late: true}, url, rec, kept, have, f.resp, f.err)
		}()
	}
}

// settle fills a call from what upstream answered: a 304 confirms the kept copy, a 200 is taken,
// a failure or a 5xx serves the kept copy stale, any other status is passed on.
func (r *Releases) settle(c *call, url string, rec storage.ReleaseRecord, kept []byte, have bool, resp upstream.Response, err error) {
	switch {
	case err != nil:
		r.stale(c, url, rec, kept, have, err)
	case resp.Status == http.StatusNotModified && have:
		_ = resp.Body.Close()
		rec.Fetched = r.Clock.Now()
		if perr := r.Store.PutRelease(rec); perr != nil {
			r.Log.Warn("release check not recorded", "url", url, "err", perr)
		}
		c.body, c.rec, c.status, c.source = kept, rec, http.StatusOK, repo.Checked
		r.Counters.ReleasesChecked.Add(1)
	case resp.Status == http.StatusOK:
		r.take(c, url, resp)
	case resp.Status >= 500:
		_ = resp.Body.Close()
		r.stale(c, url, rec, kept, have, statusError(url, resp.Status))
	default:
		_ = resp.Body.Close()
		c.status = resp.Status
	}
}

// take keeps a release file upstream just sent and learns its sums.
func (r *Releases) take(c *call, url string, resp upstream.Response) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRelease+1))
	if err != nil {
		c.err = fmt.Errorf("%s: %w: %w", url, errkind.ErrUnreachable, err)
		return
	}
	if len(body) > maxRelease {
		c.err = fmt.Errorf("%s: larger than %d bytes: %w", url, maxRelease, errkind.ErrUpstream)
		return
	}
	r.Counters.BytesFetched.Add(int64(len(body)))
	d := digest.Of(body)
	rec := storage.ReleaseRecord{URL: url, Digest: d, Fetched: r.Clock.Now(), Modified: resp.Modified, ETag: resp.ETag}
	if err := r.keep(d, body); err != nil {
		r.Log.Warn("release not stored", "url", url, "err", err)
	} else if err := r.Store.PutRelease(rec); err != nil {
		r.Log.Warn("release not recorded", "url", url, "err", err)
	}
	r.learn(url, d, body)
	c.body, c.rec, c.status, c.source = body, rec, http.StatusOK, repo.Miss
	r.Counters.ReleasesChanged.Add(1)
}

// stale answers with the kept release file when upstream could not be asked, or fails with why.
func (r *Releases) stale(c *call, url string, rec storage.ReleaseRecord, kept []byte, have bool, why error) {
	if c.late {
		r.Log.Debug("late release fetch failed", "url", url, "err", why)
		return
	}
	if !have {
		c.err = why
		return
	}
	c.body, c.rec, c.status, c.source = kept, rec, http.StatusOK, repo.Stale
	r.Counters.ReleasesStale.Add(1)
	r.Log.Warn("release served stale", "url", url, "err", why)
}

// stored reads a kept release file, teaching the catalog when it has not seen it.
func (r *Releases) stored(url string, rec storage.ReleaseRecord) ([]byte, error) {
	f, _, err := r.Store.Blob(rec.Digest)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if digest.Of(body) != rec.Digest {
		return nil, fmt.Errorf("store: release %s does not match its digest: %w", url, errkind.ErrNotFound)
	}
	if err := r.Store.Touch(rec.Digest, r.Clock.Now()); err != nil {
		r.Log.Debug("release use not recorded", "url", url, "err", err)
	}
	r.learn(url, rec.Digest, body)
	return body, nil
}

// Learn teaches the catalog a kept release file, as phortos starts; one it cannot read is skipped.
func (r *Releases) Learn(rec storage.ReleaseRecord) {
	if _, err := r.stored(rec.URL, rec); err != nil {
		r.Log.Debug("kept release not learned", "url", rec.URL, "err", err)
	}
}

// learn teaches the catalog a release file's sums, once per version. Release.gpg holds no sums.
func (r *Releases) learn(url string, d digest.Digest, body []byte) {
	if path.Base(url) == "Release.gpg" || r.Catalog.Knows(url, d.Hex()) {
		return
	}
	rel, err := release.Parse(body)
	if err != nil {
		r.Log.Warn("release file not understood", "url", url, "err", err)
		return
	}
	for _, l := range r.Catalog.Learn(url, d.Hex(), rel) {
		r.Read(l)
	}
}

// keep stores a release file's bytes under their digest, unless they are there already.
func (r *Releases) keep(d digest.Digest, body []byte) error { return keepBytes(r.Store, d, body) }

// keepBytes stores bytes held in memory under their digest, unless they are there already.
func keepBytes(store storage.Store, d digest.Digest, body []byte) error {
	if f, _, err := store.Blob(d); err == nil {
		return f.Close()
	}
	p, err := store.Begin(d)
	if err != nil {
		return err
	}
	if _, err := p.Write(body); err != nil {
		_ = p.Abort()
		return err
	}
	return p.Commit()
}

// readSeekNopCloser is an in-memory file whose Close does nothing.
type readSeekNopCloser struct{ *bytes.Reader }

// Close does nothing.
func (readSeekNopCloser) Close() error { return nil }
