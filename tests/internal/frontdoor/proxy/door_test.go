// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package proxy_test

// Verifies, with a real HTTP client using the door as its proxy:
//   - A file is served with its source, and a stored one honours ranges, HEAD and
//     If-Modified-Since
//   - A passed-through status (404) reaches the client
//   - Failures map to statuses: forbidden 403, invalid 400, not found 404, unreachable 504,
//     upstream 502
//   - CONNECT without an issuer, other methods, https:// and non-proxy requests are refused
//   - ":80" is dropped from the host, so one repository has one name

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/frontdoor/proxy"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// fetcher answers from a table of URLs; a URL missing from it fails with err.
type fetcher struct {
	files map[string][]byte
	err   error
	asked []string
}

// Serves serves everything.
func (f *fetcher) Serves(string) bool { return true }

// Get answers from the table, as a stored file (seekable).
func (f *fetcher) Get(_ context.Context, u string, _ repo.Ask) (repo.File, error) {
	f.asked = append(f.asked, u)
	b, ok := f.files[u]
	if !ok {
		return repo.File{}, f.err
	}
	if b == nil {
		return repo.File{Status: http.StatusNotFound, Size: 0, Body: io.NopCloser(bytes.NewReader(nil)), Source: repo.Passed}, nil
	}
	return repo.File{
		Status: http.StatusOK, Size: int64(len(b)), Modified: time.Date(2026, 10, 30, 6, 0, 0, 0, time.UTC),
		Body: nopSeeker{bytes.NewReader(b)}, Source: repo.Hit,
	}, nil
}

// nopSeeker is a seekable body.
type nopSeeker struct{ *bytes.Reader }

// Close does nothing.
func (nopSeeker) Close() error { return nil }

// through serves the door and gives a client whose proxy it is.
func through(t *testing.T, f *fetcher) (*http.Client, string) {
	t.Helper()
	srv := httptest.NewServer(&proxy.Door{Fetcher: f, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(srv.Close)
	p, _ := url.Parse(srv.URL)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(p)}}, srv.URL
}

func TestAFileIsServedWithRangesAndConditions(t *testing.T) {
	f := &fetcher{files: map[string][]byte{"http://repo.example/a.deb": []byte("0123456789")}}
	c, _ := through(t, f)
	resp, err := c.Get("http://repo.example:80/a.deb")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "0123456789" || resp.Header.Get("X-Phortos") != "hit" {
		t.Fatalf("%d %q %v", resp.StatusCode, body, resp.Header)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://repo.example/a.deb", nil)
	req.Header.Set("Range", "bytes=4-")
	resp, _ = c.Do(req)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "456789" {
		t.Errorf("range: %d %q", resp.StatusCode, body)
	}
	req, _ = http.NewRequest(http.MethodGet, "http://repo.example/a.deb", nil)
	req.Header.Set("If-Modified-Since", time.Date(2026, 10, 30, 7, 0, 0, 0, time.UTC).Format(http.TimeFormat))
	resp, _ = c.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("unchanged: %d", resp.StatusCode)
	}
	resp, _ = c.Head("http://repo.example/a.deb")
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength != 10 {
		t.Errorf("HEAD: %d %d", resp.StatusCode, resp.ContentLength)
	}
	if f.asked[0] != "http://repo.example/a.deb" {
		t.Errorf("asked %s", f.asked[0])
	}
}

func TestAPassedStatusReachesTheClient(t *testing.T) {
	c, _ := through(t, &fetcher{files: map[string][]byte{"http://repo.example/InRelease": nil}})
	resp, err := c.Get("http://repo.example/InRelease")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("%d", resp.StatusCode)
	}
}

func TestFailuresMapToStatuses(t *testing.T) {
	for err, want := range map[error]int{
		errkind.ErrForbidden:   http.StatusForbidden,
		errkind.ErrInvalid:     http.StatusBadRequest,
		errkind.ErrNotFound:    http.StatusNotFound,
		errkind.ErrUnreachable: http.StatusGatewayTimeout,
		errkind.ErrUpstream:    http.StatusBadGateway,
	} {
		c, _ := through(t, &fetcher{err: fmt.Errorf("x: %w", err)})
		resp, rerr := c.Get("http://repo.example/x")
		if rerr != nil {
			t.Fatal(rerr)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%v: %d, want %d", err, resp.StatusCode, want)
		}
	}
}

func TestWhatIsNoProxyGetIsRefused(t *testing.T) {
	c, base := through(t, &fetcher{})
	req, _ := http.NewRequest(http.MethodPost, "http://repo.example/x", nil)
	if resp, err := c.Do(req); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %v %v", resp, err)
	}
	if _, err := c.Get("https://repo.example/x"); err == nil {
		t.Error("CONNECT for https was accepted")
	}
	resp, err := http.Get(base + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("not a proxy request: %d", resp.StatusCode)
	}
}
