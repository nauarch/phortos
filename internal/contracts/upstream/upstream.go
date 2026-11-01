// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package upstream is the contract of the real repositories the cache fetches from. An HTTP
// adapter implements it.
package upstream

import (
	"context"
	"io"
	"time"
)

// Upstream is the real repositories.
type Upstream interface {
	// Fetch GETs a URL, following redirects but never from https to http. Any status upstream
	// answers comes back as a Response, whose Body the caller closes; an error means no answer
	// (errkind.ErrUnreachable) or a refused redirect (errkind.ErrUpstream).
	Fetch(ctx context.Context, url string, c Conditions) (Response, error)
}

// Conditions make a fetch conditional: upstream answers 304 when the file has not changed since
// Modified or still has the ETag. Zero values ask unconditionally.
type Conditions struct {
	Modified time.Time
	ETag     string
}

// Response is upstream's answer: its status, size (-1 when not given), validators, content type
// and body.
type Response struct {
	Status      int
	Size        int64
	Modified    time.Time
	ETag        string
	ContentType string
	Body        io.ReadCloser
}
