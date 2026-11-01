// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package catalog is what phortos knows about the repositories it has seen: the SHA256 and size
// every index and package file should have, by URL. Release files teach it the indexes; the
// Packages indexes it reads teach it the packages. Only a file the catalog knows is ever kept.
//
// A client with current indexes never asks for them, and one whose indexes changed asks only for
// their patches (pdiffs), so phortos does not wait for clients to bring it indexes: it reads the
// ones that can list a package it does not know (Unread), and, once it has read an architecture's
// indexes under an archive root, the new ones of every release changing there (Learn).
package catalog

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/nauarch/phortos/internal/features/cache/domain/layout"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Load is a Packages index phortos should read: its URL and sum, how it is compressed, the sum
// of its uncompressed form when the release lists one (Plain, with HasPlain), and the archive
// root URL its file names are relative to.
type Load struct {
	URL         string
	Sum         release.Sum
	Compression layout.Compression
	Plain       release.Sum
	HasPlain    bool
	Root        string
}

// index is where a Packages index sits: its release folder's URL, its path in the release, and
// the URL of its own folder (where its by-hash files live too).
type index struct {
	dir    string
	rel    string
	folder string
}

// Catalog is safe for concurrent use.
type Catalog struct {
	mu       sync.Mutex
	files    map[string]release.Sum
	packages map[string]release.Sum
	sizes    map[string]int64
	indexes  map[string][]index
	releases map[string]release.Release
	seen     map[string]string
	claimed  map[string]bool
	pending  map[string]*pending
	arches   map[string]map[string]bool
}

// pending is a Packages index being read; done closes when it is.
type pending struct {
	root string
	done chan struct{}
}

// New is an empty catalog.
func New() *Catalog {
	return &Catalog{
		files: map[string]release.Sum{}, packages: map[string]release.Sum{}, sizes: map[string]int64{}, indexes: map[string][]index{},
		releases: map[string]release.Release{}, seen: map[string]string{}, claimed: map[string]bool{},
		pending: map[string]*pending{}, arches: map[string]map[string]bool{},
	}
}

// Knows reports whether the release file at url was learned with the given SHA256 already.
func (c *Catalog) Knows(url, hex string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[url] == hex
}

// Learn records a release file fetched from url (whose own SHA256 is hex): every file it lists,
// by URL, and which of them are Packages indexes. It returns, claimed, the release's indexes of
// the architectures already read under its archive root, which the caller reads (see Claim).
func (c *Catalog) Learn(url, hex string, r release.Release) []Load {
	origin, p := Split(url)
	dir := p[:strings.LastIndex(p, "/")]
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen[url] = hex
	c.releases[origin+dir] = r
	for rel, sum := range r.Files {
		full := layout.Join(dir, rel)
		if full == "" {
			continue
		}
		c.files[origin+full] = sum
		c.sizes[sum.Hex] = sum.Size
		if layout.IsPackages(rel) {
			ix := index{dir: origin + dir, rel: rel, folder: origin + path.Dir(full)}
			if !slices.Contains(c.indexes[sum.Hex], ix) {
				c.indexes[sum.Hex] = append(c.indexes[sum.Hex], ix)
			}
		}
	}
	return c.unread(origin+dir, r, c.arches[origin+layout.ArchiveRoot(dir)])
}

// Unread claims the Packages indexes not read yet that can list the package file at url, which
// the catalog does not know: those of its architecture in the newest release of every suite
// under the same archive root. An architecture-independent package (or one whose name does not
// say its architecture) can be listed by any architecture's index, so it takes those of the
// architectures read there before, or, when none were, of those read anywhere. Each index is
// claimed once, so a file no index lists never makes phortos read anything twice.
func (c *Catalog) Unread(url string) []Load {
	origin, p := Split(url)
	arch, ok := layout.PackageArch(p)
	if !ok {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dirs := make([]string, 0, len(c.releases))
	for dir := range c.releases {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	var loads []Load
	for _, dir := range dirs {
		o, d := Split(dir)
		root := layout.ArchiveRoot(d)
		if o != origin || !strings.HasPrefix(p, root+"/") {
			continue
		}
		want := map[string]bool{arch: true}
		if arch == "" || arch == "all" {
			want["all"] = true
			read := c.arches[origin+root]
			if len(read) == 0 {
				read = c.archesAnywhere()
			}
			for a := range read {
				want[a] = true
			}
		}
		loads = append(loads, c.unread(dir, c.releases[dir], want)...)
	}
	return loads
}

// archesAnywhere are the architectures read under any archive root. The catalog's lock is held.
func (c *Catalog) archesAnywhere() map[string]bool {
	all := map[string]bool{}
	for _, read := range c.arches {
		for a := range read {
			all[a] = true
		}
	}
	return all
}

// unread claims a release's Packages indexes of the given architectures not read yet. The
// catalog's lock is held.
func (c *Catalog) unread(dir string, r release.Release, arches map[string]bool) []Load {
	if len(arches) == 0 {
		return nil
	}
	rels := make([]string, 0, len(r.Files))
	for rel := range r.Files {
		if layout.ListsArch(rel, arches) {
			rels = append(rels, rel)
		}
	}
	slices.Sort(rels)
	var loads []Load
	for _, rel := range rels {
		if l, ok := c.claim(index{dir: dir, rel: rel}); ok {
			loads = append(loads, l)
		}
	}
	return loads
}

// Lookup is the sum the file at url should have, when known: an index a release lists, or a
// package an index lists.
func (c *Catalog) Lookup(url string) (release.Sum, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.files[url]; ok {
		return s, true
	}
	s, ok := c.packages[url]
	return s, ok
}

// Index is the sum of the file at url when a release lists it.
func (c *Catalog) Index(url string) (release.Sum, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.files[url]
	return s, ok
}

// SizeOf is the size of the file with a SHA256, or -1 when no release listed it.
func (c *Catalog) SizeOf(hex string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.sizes[hex]; ok {
		return n
	}
	return -1
}

// Claim is the Packages index to read when the file with a SHA256, asked for at url, is a
// Packages index (in any compression) whose readable sibling no one has read or is reading. The
// same bytes can be the index of several folders (two architectures' empty indexes, say), so the
// folder url sits in decides which. The caller reads it and calls Done.
func (c *Catalog) Claim(hex, url string) (Load, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := slices.IndexFunc(c.indexes[hex], func(ix index) bool { return strings.HasPrefix(url, ix.folder+"/") })
	if i < 0 {
		return Load{}, false
	}
	return c.claim(c.indexes[hex][i])
}

// claim claims the readable sibling of an index when no one has read or is reading it, and
// records its architecture as read under its archive root. The catalog's lock is held.
func (c *Catalog) claim(ix index) (Load, bool) {
	r := c.releases[ix.dir]
	rel, kind, ok := layout.Readable(r.Files, ix.rel)
	if !ok {
		return Load{}, false
	}
	origin, dir := Split(ix.dir)
	if arch, ok := layout.IndexArch(rel); ok {
		root := origin + layout.ArchiveRoot(dir)
		if c.arches[root] == nil {
			c.arches[root] = map[string]bool{}
		}
		c.arches[root][arch] = true
	}
	sum := r.Files[rel]
	if c.claimed[sum.Hex] {
		return Load{}, false
	}
	c.claimed[sum.Hex] = true
	l := Load{URL: origin + layout.Join(dir, rel), Sum: sum, Compression: kind, Root: origin + layout.ArchiveRoot(dir)}
	l.Plain, l.HasPlain = layout.Uncompressed(r.Files, rel)
	c.pending[sum.Hex] = &pending{root: l.Root, done: make(chan struct{})}
	return l, true
}

// LearnPackages records the package files a Packages index lists, by their names relative to the
// load's archive root.
func (c *Catalog) LearnPackages(l Load, names []string, sums []release.Sum) {
	origin, root := Split(l.Root)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, name := range names {
		if full := layout.Join(root, name); full != "" {
			c.packages[origin+full] = sums[i]
		}
	}
}

// Done ends a claimed load. One that failed for a passing reason may be claimed again by the
// next request for its index; one upstream does not have (Debian lists an uncompressed index it
// does not serve) stays claimed, so it is not asked for again and again.
func (c *Catalog) Done(l Load, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pending[l.Sum.Hex]; ok {
		close(p.done)
		delete(c.pending, l.Sum.Hex)
	}
	if err != nil && errkind.Of(err) != errkind.NotFound {
		delete(c.claimed, l.Sum.Hex)
	}
}

// Wait waits until no Packages index under an archive root holding url is being read, or ctx
// ends.
func (c *Catalog) Wait(ctx context.Context, url string) {
	for {
		c.mu.Lock()
		var wait chan struct{}
		for _, p := range c.pending {
			if strings.HasPrefix(url, p.root+"/") {
				wait = p.done
				break
			}
		}
		c.mu.Unlock()
		if wait == nil {
			return
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return
		}
	}
}

// Split cuts a URL into its origin ("http://host") and its path ("/a/b").
func Split(url string) (origin, path string) {
	scheme, rest, ok := strings.Cut(url, "://")
	if !ok {
		return "", url
	}
	host, p, _ := strings.Cut(rest, "/")
	return scheme + "://" + host, "/" + p
}
