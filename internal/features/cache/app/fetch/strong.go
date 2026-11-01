// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/digest"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// maxStrong bounds a file known by its SHA512 only. Releases list only indexes that way, which
// are small; the file is held in memory while it is checked.
const maxStrong = 256 << 20

// Strong gives the SHA256 of a file a release lists by its SHA512 only. Such a file is downloaded
// once (requests meanwhile share the download), checked against its SHA512 and size, and kept
// under its SHA256, which is remembered: from then on it is served, evicted and fetched again
// like any other file. Downloads run on Background.
type Strong struct {
	Store      storage.Store
	Upstream   upstream.Upstream
	Background context.Context
	Counters   *Counters
	Log        *slog.Logger

	mu    sync.Mutex
	known map[string]digest.Digest
	calls map[string]*strongCall
}

// strongCall is one download of a file known by its SHA512, shared by every request for it.
type strongCall struct {
	done chan struct{}
	d    digest.Digest
	err  error
}

// Digest is the SHA256 digest of the file at url with sum: read off a SHA256 sum, or for a SHA512
// one, learned by downloading, checking and keeping the file when it has not been yet.
func (s *Strong) Digest(ctx context.Context, url string, sum release.Sum) (digest.Digest, error) {
	if !sum.SHA512 {
		return digest.Parse(digest.Algorithm + ":" + sum.Hex)
	}
	s.mu.Lock()
	if d, ok := s.known[sum.Hex]; ok {
		s.mu.Unlock()
		return d, nil
	}
	c, joined := s.calls[sum.Hex]
	if !joined {
		if s.calls == nil {
			s.known, s.calls = map[string]digest.Digest{}, map[string]*strongCall{}
		}
		c = &strongCall{done: make(chan struct{})}
		s.calls[sum.Hex] = c
		go func() {
			c.d, c.err = s.fetch(url, sum)
			s.mu.Lock()
			if c.err == nil {
				s.known[sum.Hex] = c.d
			}
			delete(s.calls, sum.Hex)
			s.mu.Unlock()
			close(c.done)
		}()
	}
	s.mu.Unlock()
	select {
	case <-c.done:
		return c.d, c.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// fetch downloads, checks and keeps a file known by its SHA512.
func (s *Strong) fetch(url string, sum release.Sum) (digest.Digest, error) {
	if sum.Size > maxStrong {
		return "", fmt.Errorf("%s: %d bytes known by SHA512 only, more than the %d phortos checks: %w", url, sum.Size, maxStrong, errkind.ErrUpstream)
	}
	resp, err := s.Upstream.Fetch(s.Background, url, upstream.Conditions{})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.Status != http.StatusOK {
		return "", statusError(url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, sum.Size+1))
	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", url, errkind.ErrUnreachable, err)
	}
	s.Counters.BytesFetched.Add(int64(len(body)))
	got := sha512.Sum512(body)
	if int64(len(body)) != sum.Size || hex.EncodeToString(got[:]) != sum.Hex {
		s.Counters.Failures.Add(1)
		return "", fmt.Errorf("%s: %d bytes not matching the SHA512 its release lists (%d bytes): %w", url, len(body), sum.Size, errkind.ErrUpstream)
	}
	d := digest.Of(body)
	if err := keepBytes(s.Store, d, body); err != nil {
		return "", err
	}
	s.Log.Info("file cached", "url", url, "size", len(body), "checked", "sha512")
	return d, nil
}
