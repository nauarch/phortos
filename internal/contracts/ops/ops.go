// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package ops is the contract of what phortos tells its operators and its clients: whether it
// is healthy, its counters, and how a client should reach a repository.
package ops

// Stats are phortos' counters since it started, and the store's size now.
type Stats struct {
	// Hits were served from the store, Joins joined a running download, Misses started one;
	// Failures are downloads that failed, stalled or did not match their sum.
	Hits     int64
	Joins    int64
	Misses   int64
	Failures int64
	// Release files: Fresh answered within their lifetime, Checked confirmed upstream, Changed
	// fetched anew, Stale served from the store because upstream failed or was too slow.
	ReleasesFresh   int64
	ReleasesChecked int64
	ReleasesChanged int64
	ReleasesStale   int64
	// Passed are requests passed through uncached.
	Passed int64
	// Prefetched are downloads started ahead of a request a client had queued; the request
	// itself still counts among the misses.
	Prefetched int64
	// PackagesRead are Packages indexes read and checked; PackagesReadFailed could not be.
	PackagesRead       int64
	PackagesReadFailed int64
	// BytesServed went to clients from the cache, BytesFetched came from upstream to be kept,
	// BytesPassed went through uncached.
	BytesServed  int64
	BytesFetched int64
	BytesPassed  int64
	// Evicted files and their bytes.
	Evicted      int64
	EvictedBytes int64
	// StoreBytes and StoreFiles are the store's size at the last eviction pass; MaxBytes its cap
	// (0 when uncapped).
	StoreBytes int64
	StoreFiles int64
	MaxBytes   int64
}

// Reporter tells phortos' health and counters.
type Reporter interface {
	// Healthy fails when phortos cannot serve, such as when its store's share is gone.
	Healthy() error
	Stats() Stats
}

// Direct is the route that sends a client straight to the repository.
const Direct = "DIRECT"

// Route is how a client should reach one repository URL: Proxy is phortos' proxy address
// ("http://phortos.lan:3142") or Direct. For an HTTPS repository phortos intercepts, CAs are the
// fingerprints of the working CAs it signs with now; a client that installed none of them goes
// direct, since phortos' certificates would fail there.
type Route struct {
	Proxy string   `json:"proxy"`
	CAs   []string `json:"cas,omitempty"`
}

// Tools are the phortosctl binaries phortos hands out: the version phortos was built as, and
// each binary's hex SHA-256 by name (phortosctl-<os>-<arch>). A host whose phortosctl is not the
// one phortos hands out for its platform is told to upgrade.
type Tools struct {
	Version  string            `json:"version"`
	Binaries map[string]string `json:"binaries"`
}
