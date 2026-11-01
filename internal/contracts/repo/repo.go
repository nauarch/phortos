// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package repo is the contract between the cache and the proxy door: a repository file asked for
// by its URL.
package repo

import (
	"context"
	"io"
	"time"
)

// Fetcher hands out repository files. Failures wrap errkind: ErrForbidden for a host not served,
// ErrInvalid for a malformed URL, ErrNotFound, ErrUpstream and ErrUnreachable.
type Fetcher interface {
	// Serves reports whether a host (lower case, with a port only when not the default) is served.
	Serves(host string) bool
	// Get is the file at url ("http://host/path"), asked for as the client asked.
	Get(ctx context.Context, url string, c Ask) (File, error)
}

// Prefetcher starts fetching a file ahead of a request for it that a client queued behind others,
// and says whether it did; it never fails, leaving whatever it cannot start to the request.
type Prefetcher interface {
	Prefetch(url string) bool
}

// Ask is how the client asked: unchanged since Modified (zero when it did not ask so).
type Ask struct {
	Modified time.Time
}

// Source is where an answer came from.
type Source string

// The sources of an answer.
const (
	// Hit: from the store.
	Hit Source = "hit"
	// Miss: downloaded now, and kept.
	Miss Source = "miss"
	// Joined: joined a download another request started.
	Joined Source = "joined"
	// Checked: a release file upstream confirmed unchanged.
	Checked Source = "checked"
	// Stale: a release file kept from before, because upstream could not be asked.
	Stale Source = "stale"
	// Passed: passed through from upstream and not kept, as it cannot be verified.
	Passed Source = "passed"
)

// File is the answer. Status is 200 for a file, else upstream's status passed through (such as
// 304 or 404). Size is -1 when unknown. Body is an io.ReadSeeker too when it comes from the store,
// so ranges can be served; the caller closes it.
type File struct {
	Status      int
	Size        int64
	Modified    time.Time
	ETag        string
	ContentType string
	Body        io.ReadCloser
	Source      Source
}
