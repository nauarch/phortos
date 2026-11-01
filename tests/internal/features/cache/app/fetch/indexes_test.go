// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch_test

// Verifies, with a repository that serves its index as xz only (as Debian's security suite):
//   - The index is decompressed, checked against the uncompressed sum its release lists, and
//     every package it lists becomes known
//   - An index that decompresses to other bytes than the release lists teaches nothing, and the
//     failure is counted

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	"github.com/nauarch/phortos/internal/adapters/storage/disk"
	"github.com/nauarch/phortos/internal/features/cache/app/catalog"
	"github.com/nauarch/phortos/internal/features/cache/app/fetch"
	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	kitclock "github.com/nauarch/phortos/tests/kit/clock"
)

// The xz fixture holding 2500 package stanzas, and the sum and size of what it decompresses to.
const (
	xzFixture  = "packages-sha256-9e.xz"
	plainHex   = "9adb1ac77738a6c199631bf1e3168942cc96d9106e30ad3c3852c1996cda08d4"
	plainSize  = 570559
	stanzas    = 2500
	securityAt = "/debian-security/dists/trixie-security"
)

// readXZIndex serves the fixture as an xz-only index under a release listing plain, reads it, and
// gives the catalog and the counters.
func readXZIndex(t *testing.T, plain release.Sum) (*catalog.Catalog, *fetch.Counters, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "kernel", "xz", "testdata", xzFixture))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != securityAt+"/main/binary-amd64/Packages.xz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	store, err := disk.Open(disk.Options{Root: t.TempDir(), Init: true})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	cat := catalog.New()
	cat.Learn(srv.URL+securityAt+"/InRelease", "ab", release.Release{Files: map[string]release.Sum{
		"main/binary-amd64/Packages.xz": {Hex: hex.EncodeToString(sum[:]), Size: int64(len(data))},
		"main/binary-amd64/Packages":    plain,
	}})
	counters := &fetch.Counters{}
	log := slog.New(slog.DiscardHandler)
	files := &fetch.Files{
		Store: store, Upstream: upstream.New(upstream.Options{}), Background: t.Context(),
		Clock: kitclock.At(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)), StallAfter: 5 * time.Second, Counters: counters, Log: log,
	}
	x := &fetch.Indexes{Files: files, Catalog: cat, Background: t.Context(), Counters: counters, Log: log}
	l, ok := cat.Claim(hex.EncodeToString(sum[:]), srv.URL+securityAt+"/main/binary-amd64/Packages.xz")
	if !ok {
		t.Fatal("the xz index was not claimed")
	}
	x.Read(l)
	cat.Wait(t.Context(), srv.URL+"/debian-security/pool/main/x.deb")
	return cat, counters, srv.URL + "/debian-security/"
}

func TestAnXZOnlyIndexIsReadAndChecked(t *testing.T) {
	cat, counters, root := readXZIndex(t, release.Sum{Hex: plainHex, Size: plainSize})
	if counters.PackagesRead.Load() != 1 {
		t.Fatalf("read %d, failed %d", counters.PackagesRead.Load(), counters.PackagesReadFailed.Load())
	}
	for _, i := range []int{1, stanzas} {
		if _, ok := cat.Lookup(fmt.Sprintf("%spool/main/p/pkg%d/pkg%d_1.%d-1_amd64.deb", root, i, i, i)); !ok {
			t.Errorf("package %d not known", i)
		}
	}
}

func TestAnIndexNotMatchingItsReleaseTeachesNothing(t *testing.T) {
	cat, counters, root := readXZIndex(t, release.Sum{Hex: plainHex, Size: plainSize + 1})
	if counters.PackagesReadFailed.Load() != 1 || counters.PackagesRead.Load() != 0 {
		t.Fatalf("read %d, failed %d", counters.PackagesRead.Load(), counters.PackagesReadFailed.Load())
	}
	if _, ok := cat.Lookup(root + "pool/main/p/pkg1/pkg1_1.1-1_amd64.deb"); ok {
		t.Error("a package of an unchecked index became known")
	}
}
