// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package hosts_test

// Verifies, end to end against a real phortos intercepting an HTTPS repository, with the host's
// files under a test folder and an apt that does what apt does (asks detect per URL, then fetches
// through phortos checking the installed bundle):
//   - A host whose own proxy settings conflict is refused, and nothing is written
//   - configure installs phortosctl, detect, the bundle (host CAs, then working CAs), the state
//     (hosts, CAs, credentialed hosts) and the apt configuration
//   - detect sends plain and intercepted repositories through phortos, and credentialed or
//     unknown hosts straight; verify passes once apt went through phortos
//   - A stale installed CA sends the intercepted host straight until refresh brings the current
//     one back; a refresh with nothing new changes nothing
//   - unconfigure lists, then removes, everything; detect then answers DIRECT

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nauarch/phortos/internal/adapters/host/files"
	"github.com/nauarch/phortos/internal/adapters/phortos/client"
	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/features/hosts"
	"github.com/nauarch/phortos/internal/features/hosts/domain/state"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
	"github.com/nauarch/phortos/internal/server"
	"github.com/nauarch/phortos/tests/kit/aptrepo"
)

// fakeApt answers apt-config with dump and runs an update the way apt does.
type fakeApt struct {
	dump  string
	host  *hosts.Hosts
	dir   files.Dir
	urls  []string
	paths []string
}

// Config is the dump.
func (a *fakeApt) Config(context.Context) (string, error) { return a.dump, nil }

// Update asks detect for each repository and fetches through phortos or straight, as apt would,
// checking intercepted hosts against the installed bundle; failures are apt's W: lines.
func (a *fakeApt) Update(ctx context.Context) (string, error) {
	var out strings.Builder
	for _, u := range a.urls {
		answer := a.host.Detect(ctx, u)
		if answer == ops.Direct {
			fmt.Fprintf(&out, "Get: %s (direct)\n", u)
			continue
		}
		if err := a.fetch(ctx, answer, u); err != nil {
			fmt.Fprintf(&out, "W: Failed to fetch %s  %v\n", u, err)
			continue
		}
		fmt.Fprintf(&out, "Hit: %s\n", u)
	}
	return out.String(), nil
}

// fetch gets u through proxy, trusting only the installed bundle.
func (a *fakeApt) fetch(ctx context.Context, proxy, u string) error {
	pem, err := a.dir.Read("/usr/local/lib/phortos/ca-bundle.pem")
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	p, _ := url.Parse(proxy)
	tp := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{RootCAs: roots}}
	defer tp.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := (&http.Client{Transport: tp}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}

// phortos runs a real phortos intercepting the TLS repository and serving the plain one, and
// gives its proxy and API addresses.
func phortos(t *testing.T, tlsRepo, plain *aptrepo.Repo) (string, string) {
	t.Helper()
	tp := tlsRepo.Transport()
	toRepo := tp.DialContext
	tp.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == aptrepo.TLSHost+":443" {
			return toRepo(ctx, network, addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	addr, apiAddr := make(chan string, 1), make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{
			Addr: "127.0.0.1:0", APIAddr: "127.0.0.1:0", Dir: t.TempDir(), Init: true, Hosts: []string{plain.Host()},
			Intercept: []string{aptrepo.TLSHost}, Keys: filepath.Join(t.TempDir(), "keys"), Transport: tp,
			Host: "test", PID: 1, Tools: fstest.MapFS{}, SelfCheck: -1,
			Listening: func(a string) { addr <- a }, APIListening: func(a string) { apiAddr <- a },
		}, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("phortos did not stop")
		}
	})
	var p, a string
	for p == "" || a == "" {
		select {
		case a = <-apiAddr:
		case p = <-addr:
		case err := <-done:
			t.Fatal(err)
		}
	}
	return p, a
}

// pinOf is the pin phortos' API presents, read the way its log tells the owner.
func pinOf(t *testing.T, api string) string {
	t.Helper()
	conn, err := tls.Dial("tcp", api, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return seal.Pin(conn.ConnectionState().PeerCertificates[0].RawSubjectPublicKeyInfo)
}

func TestAHostIsPointedAtPhortosAndBack(t *testing.T) {
	tlsRepo := aptrepo.New(aptrepo.Options{Flat: true, TLS: true})
	defer tlsRepo.Close()
	plain := aptrepo.New(aptrepo.Options{})
	defer plain.Close()
	proxy, api := phortos(t, tlsRepo, plain)
	target := hostplan.Target{Server: proxy, API: api, Pin: pinOf(t, api)}

	dir := files.Dir{Root: t.TempDir()}
	write := func(path, data string) {
		if err := dir.Write(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("/tmp/phortosctl", "the phortosctl binary")
	write("/etc/ssl/certs/ca-certificates.crt", "# the host's own CAs\n")
	write("/etc/apt/auth.conf.d/pve.conf", "machine enterprise.proxmox.com login k password s\n")
	apt := &fakeApt{dir: dir, dump: "Acquire::http::Proxy \"http://squid:3128\";\n", urls: []string{
		plain.URL() + plain.InRelease(), tlsRepo.URL() + tlsRepo.InRelease(), "http://enterprise.proxmox.com/debian/pve/dists/x/InRelease",
	}}
	h, err := hosts.Compose(hosts.Options{Files: dir, Apt: apt, Phortos: client.New(5 * time.Second), Self: "/tmp/phortosctl"})
	if err != nil {
		t.Fatal(err)
	}
	apt.host = h
	ctx := t.Context()

	if _, err := h.Configure(ctx, target); errkind.Of(err) != errkind.Conflict {
		t.Fatalf("a conflicting host: %v", err)
	}
	if _, err := dir.Read("/etc/apt/apt.conf.d/00phortos"); errkind.Of(err) != errkind.NotFound {
		t.Fatal("a refused configure wrote")
	}
	apt.dump = "Acquire::Retries \"3\";\n"
	p, err := h.Configure(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Intercepted) != 1 || len(p.CAs) != 1 || len(p.Credentialed) != 1 || len(p.Writes) != 5 {
		t.Errorf("plan %+v", p)
	}
	if b, _ := dir.Read("/usr/local/lib/phortos/phortosctl"); string(b) != "the phortosctl binary" {
		t.Error("phortosctl not installed")
	}
	if b, _ := dir.Read("/usr/local/lib/phortos/ca-bundle.pem"); !strings.HasPrefix(string(b), "# the host's own CAs\n") || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Errorf("bundle %s", b)
	}
	if b, _ := dir.Read("/etc/apt/apt.conf.d/00phortos"); !strings.Contains(string(b), "Acquire::https::example.com::CaInfo") {
		t.Errorf("apt conf %s", b)
	}

	for u, want := range map[string]string{
		plain.URL() + plain.InRelease():                           "http://" + proxy,
		tlsRepo.URL() + tlsRepo.InRelease():                       "http://" + proxy,
		"http://enterprise.proxmox.com/debian/pve/":               ops.Direct,
		"http://deb.example.org/debian/dists/trixie/InRelease":    ops.Direct,
		"https://download.docker.com/linux/debian/dists/trixie/x": ops.Direct,
	} {
		if got := h.Detect(ctx, u); got != want {
			t.Errorf("detect %s: %s, want %s", u, got, want)
		}
	}
	r, err := h.Verify(ctx)
	if err != nil || r.Requests < 2 {
		t.Errorf("verify: %+v %v", r, err)
	}

	if what, err := h.Refresh(ctx); err != nil || what != "unchanged" {
		t.Errorf("refresh: %q %v", what, err)
	}
	b, _ := dir.Read("/etc/phortos/phortosctl.json")
	st, _ := state.Decode(b)
	st.CAs[0].Fingerprint = strings.Repeat("0", 64)
	write("/etc/phortos/phortosctl.json", string(state.Encode(st)))
	if got := h.Detect(ctx, tlsRepo.URL()+tlsRepo.InRelease()); got != ops.Direct {
		t.Errorf("a stale CA: %s", got)
	}
	if what, err := h.Refresh(ctx); err != nil || !strings.Contains(what, "working CAs") {
		t.Errorf("refresh after a stale CA: %q %v", what, err)
	}
	if got := h.Detect(ctx, tlsRepo.URL()+tlsRepo.InRelease()); got != "http://"+proxy {
		t.Errorf("after refresh: %s", got)
	}

	if paths, err := h.Unconfigure(ctx, false); err != nil || len(paths) != 3 {
		t.Fatalf("%v %v", paths, err)
	}
	if _, err := dir.Read("/etc/apt/apt.conf.d/00phortos"); err != nil {
		t.Error("a dry unconfigure removed")
	}
	if _, err := h.Unconfigure(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/etc/apt/apt.conf.d/00phortos", "/etc/phortos/phortosctl.json", "/usr/local/lib/phortos/phortosctl"} {
		if _, err := dir.Read(p); errkind.Of(err) != errkind.NotFound {
			t.Errorf("%s left behind", p)
		}
	}
	if names, err := dir.List("/etc"); err != nil || slices.Contains(names, "phortos") {
		t.Errorf("/etc/phortos left behind: %v %v", names, err)
	}
	if got := h.Detect(ctx, plain.URL()+plain.InRelease()); got != ops.Direct {
		t.Errorf("after unconfigure: %s", got)
	}
}
