// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package layout knows where things sit in an apt repository's URLs: release files, files served
// by hash, the archive root package file names are relative to, and which Packages index of a
// folder phortos can read. Both repository shapes are covered: the classic one (dists/<suite>/)
// and the flat one (a single folder, as NVIDIA's CUDA repository).
package layout

import (
	"path"
	"strings"

	"github.com/nauarch/phortos/internal/features/cache/domain/release"
)

// Compression is how a Packages index phortos reads is compressed.
type Compression int

// The compressions phortos can read: gzip and bzip2 from Go's standard library, xz from the
// kernel's own decoder (zstd and lzma are not read).
const (
	Plain Compression = iota
	Gzip
	Bzip2
	Xz
)

// Tidy drops the "." elements of a URL path: a flat repository lists its packages as "./name",
// so apt asks for ".../x86_64/./name". Nothing else is changed, so ".." and doubled slashes are
// still refused by ValidPath.
func Tidy(p string) string {
	for strings.Contains(p, "/./") {
		p = strings.ReplaceAll(p, "/./", "/")
	}
	if strings.HasSuffix(p, "/.") {
		p = strings.TrimSuffix(p, ".")
	}
	return p
}

// ValidPath reports whether a URL path is one phortos will handle: absolute, already clean (no
// "..", "." or doubled slashes), at most 1024 bytes and free of control characters.
func ValidPath(p string) bool {
	if p == "" || p[0] != '/' || len(p) > 1024 || path.Clean(p) != p {
		return false
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// IsRelease reports whether a path names a release file: InRelease, Release or Release.gpg.
func IsRelease(p string) bool {
	switch path.Base(p) {
	case "InRelease", "Release", "Release.gpg":
		return true
	}
	return false
}

// ByHash is the SHA256 a by-hash path names (".../by-hash/SHA256/<hex>"), if it is one.
func ByHash(p string) (string, bool) {
	dir, hex := path.Split(p)
	if !strings.HasSuffix(dir, "/by-hash/SHA256/") || !release.IsHex(hex) {
		return "", false
	}
	return hex, true
}

// ArchiveRoot is the folder a release folder's package file names are relative to: the part
// before /dists/ in a classic repository, the release folder itself in a flat one.
func ArchiveRoot(releaseDir string) string {
	if i := strings.LastIndex(releaseDir, "/dists/"); i >= 0 {
		return releaseDir[:i]
	}
	return releaseDir
}

// Join is a path inside a folder: rel cleaned, with "./" dropped, and "" when it would climb out.
func Join(dir, rel string) string {
	p := path.Clean(path.Join(dir, rel))
	if p != dir && !strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/") {
		return ""
	}
	return p
}

// IsPackages reports whether a release file's entry is a Packages index, in any compression.
func IsPackages(rel string) bool {
	b := path.Base(rel)
	return b == "Packages" || strings.HasPrefix(b, "Packages.")
}

// PackageArch is the architecture a package file's name ends in ("name_version_arch.deb", .udeb
// or .ddeb), "all" for an architecture-independent one, and "" for a package file named
// otherwise (Grafana's "alloy-1.21.3-1.amd64.deb"), if the path names a package file at all.
func PackageArch(p string) (string, bool) {
	b := path.Base(p)
	for _, ext := range []string{".deb", ".udeb", ".ddeb"} {
		if name, ok := strings.CutSuffix(b, ext); ok {
			parts := strings.Split(name, "_")
			if len(parts) == 3 && parts[0] != "" && parts[2] != "" {
				return parts[2], true
			}
			return "", name != ""
		}
	}
	return "", false
}

// IndexArch is the architecture of a release file's entry in a binary-<arch> folder.
func IndexArch(rel string) (string, bool) {
	return strings.CutPrefix(path.Base(path.Dir(rel)), "binary-")
}

// ListsArch reports whether a release file's entry is a Packages index that can list packages of
// one of arches: a component's binary-<arch> index (<component>/binary-<arch>/Packages*, not the
// installer's deeper ones), or a flat repository's single index, which lists them all.
func ListsArch(rel string, arches map[string]bool) bool {
	if !IsPackages(rel) {
		return false
	}
	dir := path.Dir(rel)
	if dir == "." {
		return true
	}
	arch, ok := IndexArch(rel)
	return ok && strings.Count(dir, "/") == 1 && arches[arch]
}

// Readable is the Packages index of the same folder as rel that phortos can read, as the release
// lists it: gzip, then bzip2, then xz, then uncompressed. xz counts only when the release also
// lists the uncompressed index, whose sum checks what phortos' own decoder made of it. Debian's
// security and updates suites list an uncompressed index they do not serve, so it comes last.
func Readable(files map[string]release.Sum, rel string) (string, Compression, bool) {
	dir := path.Dir(rel)
	_, plain := files[path.Join(dir, "Packages")]
	for _, c := range []struct {
		name string
		kind Compression
		ok   bool
	}{{"Packages.gz", Gzip, true}, {"Packages.bz2", Bzip2, true}, {"Packages.xz", Xz, plain}, {"Packages", Plain, true}} {
		p := path.Join(dir, c.name)
		if _, listed := files[p]; listed && c.ok {
			return p, c.kind, true
		}
	}
	return "", Plain, false
}

// Uncompressed is the uncompressed Packages index of the same folder as rel, when the release
// lists it: the sum what any compressed index decompresses to must have.
func Uncompressed(files map[string]release.Sum, rel string) (release.Sum, bool) {
	s, ok := files[path.Join(path.Dir(rel), "Packages")]
	return s, ok
}
