// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package catalog_test

// Verifies:
//   - A release teaches the sum of every file it lists, by URL, in both repository shapes
//   - A Packages index in any compression claims its readable sibling once; a read that failed
//     for a passing reason can be claimed again, one upstream lacks cannot
//   - Identical index bytes in two folders (two architectures' empty indexes) claim the folder
//     the request is in
//   - The packages an index lists become known under the archive root, never outside it
//   - An unknown package has the indexes of its architecture read, in every suite under its
//     archive root, once; an architecture-independent one (or one whose name does not say it)
//     those of the architectures read there before, else of those read anywhere, and a file
//     that is no package nothing
//   - A release that changes has the indexes of the architectures read before read at once
//   - Waiting for an unknown file waits only for reads under its archive root
//   - URLs split into origin and path

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/domain/layout"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

const (
	hexXZ  = "1111111111111111111111111111111111111111111111111111111111111111"
	hexGZ  = "2222222222222222222222222222222222222222222222222222222222222222"
	hexDeb = "3333333333333333333333333333333333333333333333333333333333333333"
)

// classic is a Debian-shaped release.
func classic() release.Release {
	return release.Release{Files: map[string]release.Sum{
		"main/binary-amd64/Packages.xz": {Hex: hexXZ, Size: 10},
		"main/binary-amd64/Packages.gz": {Hex: hexGZ, Size: 20},
	}}
}

func TestAReleaseTeachesItsFiles(t *testing.T) {
	c := catalog.New()
	c.Learn("http://deb.debian.org/debian/dists/trixie/InRelease", "ab", classic())
	if s, ok := c.Lookup("http://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.gz"); !ok || s.Hex != hexGZ {
		t.Errorf("%v %v", s, ok)
	}
	if !c.Knows("http://deb.debian.org/debian/dists/trixie/InRelease", "ab") || c.Knows("http://deb.debian.org/debian/dists/trixie/InRelease", "cd") {
		t.Error("Knows is wrong")
	}
	if c.SizeOf(hexXZ) != 10 || c.SizeOf(hexDeb) != -1 {
		t.Error("SizeOf is wrong")
	}
}

// amd64 is where the classic release's index folder sits.
const amd64 = "http://deb.debian.org/debian/dists/trixie/main/binary-amd64/"

func TestAPackagesIndexIsClaimedOnce(t *testing.T) {
	c := catalog.New()
	c.Learn("http://deb.debian.org/debian/dists/trixie/InRelease", "ab", classic())
	l, ok := c.Claim(hexXZ, amd64+"by-hash/SHA256/"+hexXZ)
	if !ok || l.URL != amd64+"Packages.gz" || l.Compression != layout.Gzip || l.Root != "http://deb.debian.org/debian" {
		t.Fatalf("%+v %v", l, ok)
	}
	if _, ok := c.Claim(hexGZ, amd64+"Packages.gz"); ok {
		t.Error("claimed twice")
	}
	c.Done(l, context.Canceled)
	l, ok = c.Claim(hexGZ, amd64+"Packages.gz")
	if !ok {
		t.Fatal("a failed read could not be claimed again")
	}
	c.Done(l, fmt.Errorf("gone: %w", errkind.ErrNotFound))
	if _, ok := c.Claim(hexGZ, amd64+"Packages.gz"); ok {
		t.Error("an index upstream lacks was claimed again")
	}
	if _, ok := c.Claim(hexDeb, amd64+"x.deb"); ok {
		t.Error("a file that is no index was claimed")
	}
}

func TestTheSameIndexBytesInTwoFoldersClaimTheAskedOne(t *testing.T) {
	const hexGZ2 = "4444444444444444444444444444444444444444444444444444444444444444"
	c := catalog.New()
	c.Learn("http://deb.debian.org/debian/dists/trixie-updates/InRelease", "ab", release.Release{Files: map[string]release.Sum{
		"main/binary-amd64/Packages.xz": {Hex: hexXZ, Size: 10}, "main/binary-amd64/Packages.gz": {Hex: hexGZ, Size: 20},
		"main/binary-armel/Packages.xz": {Hex: hexXZ, Size: 10}, "main/binary-armel/Packages.gz": {Hex: hexGZ2, Size: 20},
	}})
	base := "http://deb.debian.org/debian/dists/trixie-updates/main/"
	l, ok := c.Claim(hexXZ, base+"binary-amd64/by-hash/SHA256/"+hexXZ)
	if !ok || l.URL != base+"binary-amd64/Packages.gz" {
		t.Errorf("amd64: %+v", l)
	}
	l, ok = c.Claim(hexXZ, base+"binary-armel/Packages.xz")
	if !ok || l.URL != base+"binary-armel/Packages.gz" {
		t.Errorf("armel: %+v", l)
	}
}

func TestPackagesBecomeKnownUnderTheirRoot(t *testing.T) {
	c := catalog.New()
	c.Learn("http://dl.example/repo/x86_64/InRelease", "ab", release.Release{Files: map[string]release.Sum{"Packages.gz": {Hex: hexGZ, Size: 20}}})
	l, ok := c.Claim(hexGZ, "http://dl.example/repo/x86_64/Packages.gz")
	if !ok || l.Root != "http://dl.example/repo/x86_64" {
		t.Fatalf("%+v", l)
	}
	c.LearnPackages(l, []string{"./a_1_all.deb", "../../escape.deb"}, []release.Sum{{Hex: hexDeb, Size: 5}, {Hex: hexDeb, Size: 5}})
	c.Done(l, nil)
	if s, ok := c.Lookup("http://dl.example/repo/x86_64/a_1_all.deb"); !ok || s.Size != 5 {
		t.Error("package not known")
	}
	if _, ok := c.Lookup("http://dl.example/escape.deb"); ok {
		t.Error("a package outside the root became known")
	}
}

// debian is Debian's archive root, and suite a release of it listing an index per architecture.
const debian = "http://deb.debian.org/debian"

func suite(salt string) release.Release {
	files := map[string]release.Sum{}
	for i, rel := range []string{"main/binary-amd64/Packages.gz", "main/binary-arm64/Packages.gz", "contrib/binary-amd64/Packages.gz", "main/debian-installer/binary-amd64/Packages.gz"} {
		files[rel] = release.Sum{Hex: fmt.Sprintf("%s%063x", salt, i), Size: 20}
	}
	return release.Release{Files: files}
}

// urls are the loads' URLs.
func urls(loads []catalog.Load) []string {
	out := make([]string, 0, len(loads))
	for _, l := range loads {
		out = append(out, l.URL)
	}
	return out
}

func TestAnUnknownPackageHasTheIndexesThatCanListItRead(t *testing.T) {
	c := catalog.New()
	c.Learn(debian+"/dists/trixie/InRelease", "ab", suite("a"))
	c.Learn(debian+"/dists/trixie-updates/InRelease", "ab", suite("b"))
	c.Learn("http://security.debian.org/debian-security/dists/trixie-security/InRelease", "ab", suite("c"))
	if loads := c.Unread(debian + "/pool/main/t/tzdata/tzdata_2025b-4_all.deb"); len(loads) != 0 {
		t.Errorf("all, before any architecture was read: %v", urls(loads))
	}
	loads := c.Unread(debian + "/pool/main/c/curl/curl_8.14.1-2_amd64.deb")
	want := []string{
		debian + "/dists/trixie/contrib/binary-amd64/Packages.gz", debian + "/dists/trixie/main/binary-amd64/Packages.gz",
		debian + "/dists/trixie-updates/contrib/binary-amd64/Packages.gz", debian + "/dists/trixie-updates/main/binary-amd64/Packages.gz",
	}
	if fmt.Sprint(urls(loads)) != fmt.Sprint(want) {
		t.Errorf("amd64: %v", urls(loads))
	}
	if again := c.Unread(debian + "/pool/main/w/wget/wget_1.25_amd64.deb"); len(again) != 0 {
		t.Errorf("read twice: %v", urls(again))
	}
	if loads := c.Unread(debian + "/pool/main/c/curl/curl_8.14.1-2.dsc"); len(loads) != 0 {
		t.Errorf("not a package: %v", urls(loads))
	}
	if loads := c.Unread("http://ftp.debian.org/debian/pool/main/c/curl/curl_8.14.1-2_amd64.deb"); len(loads) != 0 {
		t.Errorf("another host's archive: %v", urls(loads))
	}
	for _, l := range loads {
		c.Done(l, nil)
	}
	c.Learn("http://security.debian.org/debian-security/dists/trixie-security/InRelease", "cd", suite("d"))
	security := "http://security.debian.org/debian-security/dists/trixie-security/"
	loads = c.Unread("http://security.debian.org/debian-security/pool/updates/main/t/tzdata/tzdata_2025c_all.deb")
	if fmt.Sprint(urls(loads)) != fmt.Sprint([]string{security + "contrib/binary-amd64/Packages.gz", security + "main/binary-amd64/Packages.gz"}) {
		t.Errorf("all, with no architecture read under this root but amd64 elsewhere: %v", urls(loads))
	}
}

func TestAPackageNamedWithoutItsArchitectureHasTheArchitecturesReadBeforeRead(t *testing.T) {
	c := catalog.New()
	c.Learn(debian+"/dists/trixie/InRelease", "ab", suite("a"))
	for _, l := range c.Unread(debian + "/pool/main/c/curl/curl_8.14.1-2_arm64.deb") {
		c.Done(l, nil)
	}
	c.Learn("https://apt.grafana.com/dists/stable/InRelease", "ab", suite("b"))
	loads := c.Unread("https://apt.grafana.com/pool/main/a/alloy/alloy-1.21.3-1.arm64.deb")
	if fmt.Sprint(urls(loads)) != fmt.Sprint([]string{"https://apt.grafana.com/dists/stable/main/binary-arm64/Packages.gz"}) {
		t.Errorf("%v", urls(loads))
	}
}

func TestANewReleaseHasTheArchitecturesReadBeforeReadAtOnce(t *testing.T) {
	c := catalog.New()
	if loads := c.Learn(debian+"/dists/trixie/InRelease", "ab", suite("a")); len(loads) != 0 {
		t.Errorf("nothing read yet: %v", urls(loads))
	}
	for _, l := range c.Unread(debian + "/pool/main/c/curl/curl_8.14.1-2_arm64.deb") {
		c.Done(l, nil)
	}
	loads := c.Learn(debian+"/dists/trixie/InRelease", "cd", suite("e"))
	if fmt.Sprint(urls(loads)) != fmt.Sprint([]string{debian + "/dists/trixie/main/binary-arm64/Packages.gz"}) {
		t.Errorf("changed release: %v", urls(loads))
	}
	for _, l := range loads {
		c.Done(l, nil)
	}
	if loads := c.Unread(debian + "/pool/main/t/tzdata/tzdata_2025b-4_all.deb"); len(loads) != 0 {
		t.Errorf("all, with arm64 read already: %v", urls(loads))
	}
}

func TestWaitingIsOnlyForReadsUnderTheRoot(t *testing.T) {
	c := catalog.New()
	c.Learn("http://deb.debian.org/debian/dists/trixie/InRelease", "ab", classic())
	l, _ := c.Claim(hexGZ, amd64+"Packages.gz")
	start := time.Now()
	c.Wait(t.Context(), "http://other.example/debian/pool/a.deb")
	if time.Since(start) > 100*time.Millisecond {
		t.Error("waited for an unrelated root")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.Done(l, nil)
	}()
	c.Wait(t.Context(), "http://deb.debian.org/debian/pool/main/a.deb")

	stuck := catalog.New()
	stuck.Learn("http://deb.debian.org/debian/dists/trixie/InRelease", "ab", classic())
	if _, ok := stuck.Claim(hexGZ, amd64+"Packages.gz"); !ok {
		t.Fatal("not claimed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start = time.Now()
	stuck.Wait(ctx, "http://deb.debian.org/debian/pool/main/a.deb")
	if time.Since(start) > time.Second {
		t.Error("waiting outlived its context")
	}
}

func TestURLsSplitIntoOriginAndPath(t *testing.T) {
	if o, p := catalog.Split("http://h:8080/a/b"); o != "http://h:8080" || p != "/a/b" {
		t.Error(o, p)
	}
}
