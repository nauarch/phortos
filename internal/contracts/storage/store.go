// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package storage is the contract of where the cache keeps what it fetched: files by their
// SHA256, and which file each release URL last held. A disk adapter implements it.
package storage

import (
	"io"
	"time"

	"github.com/nauarch/phortos/internal/kernel/digest"
)

// Store keeps files by digest and what release URLs held. Missing things are errkind.ErrNotFound.
type Store interface {
	// Blob opens a committed file and tells its size.
	Blob(d digest.Digest) (io.ReadSeekCloser, int64, error)
	// Begin starts writing a file; nothing is visible until the Pending is committed.
	Begin(d digest.Digest) (Pending, error)
	// Touch records that a file was used at now, for eviction to keep the most used.
	Touch(d digest.Digest, now time.Time) error
	// Blobs lists every committed file with its size and last use.
	Blobs() ([]Entry, error)
	// RemoveBlob deletes a committed file.
	RemoveBlob(d digest.Digest) error
	// Release reads what a release file's URL held when it was last fetched.
	Release(url string) (ReleaseRecord, error)
	// PutRelease records what a release file's URL holds now.
	PutRelease(r ReleaseRecord) error
	// Releases lists every recorded release file.
	Releases() ([]ReleaseRecord, error)
	// Check fails when the store cannot be written to, so health checks see a lost share.
	Check() error
}

// Pending is a file being written. Its bytes can be read back while it is written, so requests
// that join a running download stream what has arrived. Commit makes it visible under its digest;
// Abort throws it away. Either closes it.
type Pending interface {
	io.Writer
	io.ReaderAt
	Commit() error
	Abort() error
}

// Entry is a committed file as eviction sees it.
type Entry struct {
	Digest digest.Digest
	Size   int64
	Used   time.Time
}

// ReleaseRecord is what a release file's URL (InRelease, Release, Release.gpg) held when it was
// last fetched: the stored file's digest, when it was fetched, and the validators upstream gave
// it, so the next fetch can ask whether it changed.
type ReleaseRecord struct {
	URL      string        `json:"url"`
	Digest   digest.Digest `json:"digest"`
	Fetched  time.Time     `json:"fetched"`
	Modified time.Time     `json:"modified,omitzero"`
	ETag     string        `json:"etag,omitempty"`
}
