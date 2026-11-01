// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package layout_test

// Verifies:
//   - Only clean, absolute paths without control characters are handled; "." elements (a flat
//     repository's "./name") are tidied away first, nothing else is
//   - Release files and by-hash paths are recognised
//   - Package file names are relative to the part before /dists/, or to a flat repository's
//     folder, and joining refuses to climb out
//   - Packages indexes are recognised in any compression, and the readable one of a folder is
//     gzip, then bzip2, then xz (only with an uncompressed sum to check it), then uncompressed
//   - A package file's architecture comes from its name, when the name says it; an index lists
//     an architecture when it is a component's binary-<arch> index of it, or a flat
//     repository's only one

import (
	"testing"

	"github.com/nauarch/phortos/internal/features/cache/domain/layout"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
)

const hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestOnlyCleanPathsAreValid(t *testing.T) {
	for p, want := range map[string]bool{
		"/debian/dists/trixie/InRelease": true,
		"/a/b.deb":                       true,
		"":                               false,
		"relative":                       false,
		"/a/../b":                        false,
		"/a//b":                          false,
		"/a/./b":                         false,
		"/a/b/":                          false,
		"/a\nb":                          false,
	} {
		if got := layout.ValidPath(p); got != want {
			t.Errorf("%q: %v", p, got)
		}
	}
}

func TestDotElementsAreTidied(t *testing.T) {
	for p, want := range map[string]string{
		"/repo/x86_64/./a.deb": "/repo/x86_64/a.deb",
		"/a/././b":             "/a/b",
		"/a/.":                 "/a/",
		"/a/../b":              "/a/../b",
		"/a//b":                "/a//b",
		"/a/.b/c.":             "/a/.b/c.",
		"":                     "",
	} {
		if got := layout.Tidy(p); got != want {
			t.Errorf("%q: %q, want %q", p, got, want)
		}
	}
}

func TestReleaseFilesAndByHashPathsAreRecognised(t *testing.T) {
	for _, p := range []string{"/debian/dists/trixie/InRelease", "/x/Release", "/x/Release.gpg"} {
		if !layout.IsRelease(p) {
			t.Error(p)
		}
	}
	if layout.IsRelease("/x/Packages") || layout.IsRelease("/x/InRelease.bak") {
		t.Error("not release files")
	}
	if hex, ok := layout.ByHash("/debian/dists/trixie/main/binary-amd64/by-hash/SHA256/" + hexA); !ok || hex != hexA {
		t.Error("by-hash missed")
	}
	if _, ok := layout.ByHash("/x/by-hash/MD5Sum/" + hexA[:32]); ok {
		t.Error("only SHA256 by-hash paths count")
	}
}

func TestPackageNamesAreRelativeToTheArchiveRoot(t *testing.T) {
	if got := layout.ArchiveRoot("/debian/dists/trixie"); got != "/debian" {
		t.Error(got)
	}
	if got := layout.ArchiveRoot("/linux/debian/dists/bookworm"); got != "/linux/debian" {
		t.Error(got)
	}
	if got := layout.ArchiveRoot("/compute/cuda/repos/ubuntu2404/x86_64"); got != "/compute/cuda/repos/ubuntu2404/x86_64" {
		t.Error(got)
	}
	if got := layout.Join("/repo", "./a_1_all.deb"); got != "/repo/a_1_all.deb" {
		t.Error(got)
	}
	if got := layout.Join("/repo", "../etc/passwd"); got != "" {
		t.Error(got)
	}
}

func TestTheReadablePackagesIndexIsChosen(t *testing.T) {
	sum := release.Sum{Hex: hexA, Size: 1}
	files := map[string]release.Sum{
		"main/binary-amd64/Packages.xz": sum, "main/binary-amd64/Packages.gz": sum, "main/binary-amd64/Packages": sum,
		"Packages.bz2": sum, "Packages.xz": sum,
	}
	if !layout.IsPackages("main/binary-amd64/Packages.xz") || layout.IsPackages("main/i18n/Translation-en") {
		t.Error("IsPackages is wrong")
	}
	if p, c, ok := layout.Readable(files, "main/binary-amd64/Packages.xz"); !ok || p != "main/binary-amd64/Packages.gz" || c != layout.Gzip {
		t.Error(p, c, ok)
	}
	if p, c, ok := layout.Readable(files, "Packages.xz"); !ok || p != "Packages.bz2" || c != layout.Bzip2 {
		t.Error(p, c, ok)
	}
	if _, _, ok := layout.Readable(map[string]release.Sum{"Packages.xz": sum}, "Packages.xz"); ok {
		t.Error("xz without an uncompressed sum to check it against is not readable")
	}
	security := map[string]release.Sum{"main/binary-amd64/Packages.xz": sum, "main/binary-amd64/Packages": {Hex: hexA, Size: 99}}
	if p, c, ok := layout.Readable(security, "main/binary-amd64/Packages.xz"); !ok || p != "main/binary-amd64/Packages.xz" || c != layout.Xz {
		t.Error(p, c, ok)
	}
	if s, ok := layout.Uncompressed(security, "main/binary-amd64/Packages.xz"); !ok || s.Size != 99 {
		t.Error(s, ok)
	}
}

func TestAPackagesArchitectureIsInItsName(t *testing.T) {
	for p, want := range map[string]struct {
		arch string
		pkg  bool
	}{
		"/debian/pool/main/c/curl/curl_8.14.1-2_amd64.deb":         {"amd64", true},
		"/debian/pool/main/t/tzdata/tzdata_2025b-4_all.deb":        {"all", true},
		"/debian/pool/main/b/busybox/busybox-udeb_1.37_arm64.udeb": {"arm64", true},
		"/repo/x86_64/cuda-toolkit-13-0_13.0.2-1_amd64.deb":        {"amd64", true},
		"/pool/main/a/alloy/alloy-1.21.3-1.amd64.deb":              {"", true},
		"/debian/pool/main/c/curl/_1_amd64.deb":                    {"", true},
		"/debian/pool/main/c/curl/curl_8.14.1-2.dsc":               {"", false},
		"/debian/pool/main/c/curl/.deb":                            {"", false},
		"/debian/README":                                           {"", false},
	} {
		if got, ok := layout.PackageArch(p); got != want.arch || ok != want.pkg {
			t.Errorf("%s: %q %v", p, got, ok)
		}
	}
}

func TestAnIndexListsItsArchitectureOnly(t *testing.T) {
	amd64 := map[string]bool{"amd64": true}
	for rel, want := range map[string]bool{
		"main/binary-amd64/Packages.xz":             true,
		"pve-no-subscription/binary-amd64/Packages": true,
		"Packages.gz":                                    true,
		"main/binary-arm64/Packages.xz":                  false,
		"main/debian-installer/binary-amd64/Packages.xz": false,
		"main/binary-amd64/Release":                      false,
		"main/source/Sources.xz":                         false,
		"main/Contents-amd64.gz":                         false,
	} {
		if got := layout.ListsArch(rel, amd64); got != want {
			t.Errorf("%s: %v", rel, got)
		}
	}
	if a, ok := layout.IndexArch("main/binary-all/Packages.xz"); !ok || a != "all" {
		t.Error(a, ok)
	}
	if _, ok := layout.IndexArch("Packages.gz"); ok {
		t.Error("a flat index has no architecture")
	}
}
