// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"

	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/domain/layout"
	"github.com/nauarch/phortos/internal/features/cache/domain/packages"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/xz"
)

// Indexes reads Packages indexes in the background, so the catalog learns the sums of the
// packages they list. The index is fetched through Files, so it is verified against its release
// and kept, and a client asking for the same file joins the same download. What it decompresses
// to is checked against the uncompressed index's sum when the release lists one, and nothing it
// lists is learned unless that check passes. An index (or uncompressed sum) a release lists by
// SHA512 only is checked against that, through Strong.
type Indexes struct {
	Files      *Files
	Strong     *Strong
	Catalog    *catalog.Catalog
	Background context.Context
	Counters   *Counters
	Log        *slog.Logger
}

// Read starts reading a claimed index; the catalog hears when it is done.
func (x *Indexes) Read(l catalog.Load) {
	go func() {
		err := x.read(l)
		if err != nil {
			x.Counters.PackagesReadFailed.Add(1)
			x.Log.Warn("packages index not read", "url", l.URL, "err", err)
		}
		x.Catalog.Done(l, err)
	}()
}

// read fetches, decompresses, checks and reads one index.
func (x *Indexes) read(l catalog.Load) error {
	d, err := x.Strong.Digest(x.Background, l.URL, l.Sum)
	if err != nil {
		return err
	}
	f, err := x.Files.Get(x.Background, l.URL, d, l.Sum.Size)
	if err != nil {
		return err
	}
	defer f.Body.Close()
	plain, err := decompress(f.Body, l.Compression)
	if err != nil {
		return err
	}
	defer plain.Close()
	h, alg := sha256.New(), "sha256"
	if l.Plain.SHA512 {
		h, alg = sha512.New(), "sha512"
	}
	counted := &countingReader{r: io.TeeReader(plain, h)}
	var names []string
	var sums []release.Sum
	if err := packages.Read(counted, func(name string, s release.Sum) {
		names = append(names, name)
		sums = append(sums, s)
	}); err != nil {
		return err
	}
	if _, err := io.Copy(io.Discard, counted); err != nil {
		return err
	}
	if l.HasPlain {
		if got := hex.EncodeToString(h.Sum(nil)); got != l.Plain.Hex || counted.n != l.Plain.Size {
			return fmt.Errorf("%s decompresses to %d bytes with %s %s, the release lists %d bytes with %s: %w",
				l.URL, counted.n, alg, got, l.Plain.Size, l.Plain.Hex, errkind.ErrUpstream)
		}
	}
	x.Catalog.LearnPackages(l, names, sums)
	x.Counters.PackagesRead.Add(1)
	x.Log.Info("packages index read", "url", l.URL, "packages", len(names), "checked", l.HasPlain)
	return nil
}

// decompress reads an index in its compression.
func decompress(r io.Reader, c layout.Compression) (io.ReadCloser, error) {
	switch c {
	case layout.Gzip:
		return gzip.NewReader(r)
	case layout.Bzip2:
		return io.NopCloser(bzip2.NewReader(r)), nil
	case layout.Xz:
		pr, pw := io.Pipe()
		go func() { pw.CloseWithError(xz.Decode(pw, r)) }()
		return pr, nil
	}
	return io.NopCloser(r), nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

// Read reads and counts.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
