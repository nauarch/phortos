// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package upstream_test

// Verifies:
//   - A fetch sends its conditions and gets any status back with the validators and the body
//     exactly as sent, gzip included
//   - A redirect from https to http is refused as an upstream failure; one from http to https is
//     followed
//   - A repository that cannot be reached is unreachable
//   - A repository offering HTTP/2 is still asked over HTTP/1.1, a connection per download

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/adapters/repo/upstream"
	contract "github.com/nauarch/phortos/internal/contracts/upstream"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAFetchSendsItsConditionsAndKeepsTheBytes(t *testing.T) {
	modified := time.Date(2026, 10, 30, 7, 55, 41, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` && r.Header.Get("If-Modified-Since") == modified.Format(http.TimeFormat) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte("\x1f\x8b raw gzip bytes"))
	}))
	defer srv.Close()
	up := upstream.New(upstream.Options{})
	resp, err := up.Fetch(t.Context(), srv.URL+"/x/Packages.gz", contract.Conditions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Status != 200 || string(body) != "\x1f\x8b raw gzip bytes" || resp.ETag != `"v1"` || !resp.Modified.Equal(modified) {
		t.Fatalf("%+v %q", resp, body)
	}
	again, err := up.Fetch(t.Context(), srv.URL+"/x/Packages.gz", contract.Conditions{Modified: modified, ETag: `"v1"`})
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Body.Close()
	if again.Status != http.StatusNotModified {
		t.Errorf("conditional fetch: %d", again.Status)
	}
}

func TestRedirectsNeverDowngradeToHTTP(t *testing.T) {
	var secure *httptest.Server
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/up" {
			http.Redirect(w, r, secure.URL+"/file", http.StatusMovedPermanently)
			return
		}
		_, _ = w.Write([]byte("plain"))
	}))
	defer plain.Close()
	secure = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			http.Redirect(w, r, plain.URL+"/file", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("secure"))
	}))
	defer secure.Close()
	transport := secure.Client().Transport.(*http.Transport).Clone()
	up := upstream.New(upstream.Options{Transport: transport})

	if _, err := up.Fetch(t.Context(), secure.URL+"/down", contract.Conditions{}); errkind.Of(err) != errkind.Upstream {
		t.Errorf("https to http: %v", err)
	}
	resp, err := up.Fetch(t.Context(), plain.URL+"/up", contract.Conditions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "secure" {
		t.Errorf("http to https: %q", body)
	}
}

func TestRepositoriesAreAskedOverHTTP1EvenWhenTheyOfferHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	resp, err := srv.Client().Get(srv.URL + "/control")
	if err != nil {
		t.Fatal(err)
	}
	control, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(control) != "HTTP/2.0" {
		t.Fatalf("the test server does not offer HTTP/2: %s", control)
	}
	// The real transport checks certificates against the system's CAs: make the test's one of them.
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	up := upstream.New(upstream.Options{})
	got, err := up.Fetch(t.Context(), srv.URL+"/x.deb", contract.Conditions{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if string(body) != "HTTP/1.1" {
		t.Errorf("asked over %s", body)
	}
}

func TestARepositoryOutOfReachIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	up := upstream.New(upstream.Options{Timeout: 2 * time.Second})
	if _, err := up.Fetch(t.Context(), url+"/InRelease", contract.Conditions{}); errkind.Of(err) != errkind.Unreachable {
		t.Errorf("got %v", err)
	}
}
