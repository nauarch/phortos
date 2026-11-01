// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package setup_test

// Verifies:
//   - /setup is a script for phortos as the host reached its API: the pin, the API address, the
//     proxy on the same host at the proxy's port, and each binary's checksum; sh reads it
//   - The one-liner carries the pin and the API address
//   - /get hands out the binaries phortos carries, and nothing else; /get/tools names phortos'
//     version and each binary's checksum
//   - A Host header that is not a plain host:port is refused; a phortos with no binaries says so
//   (The script running for real, pinned curl and all, is the lab's to check.)

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/frontdoor/setup"
)

const pin = "sha256//Xkzycv7fSXYCqIEY8Q50/zaXigAGDDXiO3Stf1ffP9g="

// tools are two fake binaries.
var tools = fstest.MapFS{
	"phortosctl-linux-amd64": {Data: []byte("amd64 binary")},
	"phortosctl-linux-arm64": {Data: []byte("arm64 binary")},
	"README.txt":             {Data: []byte("not a binary")},
}

// get asks the door for a path with a Host header.
func get(t *testing.T, h http.Handler, host, path string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://phortos.test"+path, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Body)
	return w.Code, string(body)
}

// sum is a hex SHA-256.
func sum(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}

func TestTheScriptNamesPhortosAndTheChecksums(t *testing.T) {
	h := setup.New(setup.Options{Files: tools, Pin: pin, ProxyPort: "3142"})
	code, script := get(t, h, "phortos.lan:3143", "/setup")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, script)
	}
	for _, want := range []string{
		"api='phortos.lan:3143'", "server='phortos.lan:3142'", "pin='" + pin + "'",
		"linux-amd64) sum=" + sum("amd64 binary"), "linux-arm64) sum=" + sum("arm64 binary"),
		`curl -fsSL -k --pinnedpubkey "$pin"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(script, "README") {
		t.Error("a file that is no binary is offered")
	}
	if _, script6 := get(t, h, "[fd00::5]:3143", "/setup"); !strings.Contains(script6, "server='[fd00::5]:3142'") {
		t.Error("an IPv6 host is not kept in brackets")
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		file := filepath.Join(t.TempDir(), "setup.sh")
		if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sh, "-n", file).CombinedOutput(); err != nil {
			t.Errorf("sh cannot read the script: %v %s", err, out)
		}
	}
}

func TestTheOneLinerCarriesThePin(t *testing.T) {
	want := "curl -fsSL -k --pinnedpubkey '" + pin + "' https://phortos.lan:3143/setup | sudo sh -s -- configure --yes"
	if got := setup.Command("phortos.lan:3143", pin); got != want {
		t.Error(got)
	}
}

func TestOnlyTheBinariesAreHandedOut(t *testing.T) {
	h := setup.New(setup.Options{Files: tools, Pin: pin, ProxyPort: "3142"})
	if code, body := get(t, h, "phortos.lan:3143", "/get/phortosctl-linux-arm64"); code != http.StatusOK || body != "arm64 binary" {
		t.Errorf("%d %q", code, body)
	}
	for _, p := range []string{"/get/README.txt", "/get/phortosctl-windows-amd64.exe", "/get/..%2fsecret"} {
		if code, _ := get(t, h, "phortos.lan:3143", p); code != http.StatusNotFound {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestToolsNameTheVersionAndTheChecksums(t *testing.T) {
	h := setup.New(setup.Options{Files: tools, Pin: pin, ProxyPort: "3142", Version: "v1.2.3"})
	code, body := get(t, h, "phortos.lan:3143", "/get/tools")
	var got ops.Tools
	if err := json.Unmarshal([]byte(body), &got); code != http.StatusOK || err != nil {
		t.Fatalf("%d %s %v", code, body, err)
	}
	if got.Version != "v1.2.3" || len(got.Binaries) != 2 || got.Binaries["phortosctl-linux-amd64"] != sum("amd64 binary") {
		t.Errorf("%+v", got)
	}
}

func TestOddHostsAndMissingBinariesAreRefused(t *testing.T) {
	h := setup.New(setup.Options{Files: tools, Pin: pin, ProxyPort: "3142"})
	if code, _ := get(t, h, "phortos.lan:3143';rm -rf /;'", "/setup"); code != http.StatusBadRequest {
		t.Errorf("odd host: %d", code)
	}
	empty := setup.New(setup.Options{Files: fstest.MapFS{}, Pin: pin, ProxyPort: "3142"})
	if code, body := get(t, empty, "phortos.lan:3143", "/setup"); code != http.StatusServiceUnavailable || !strings.Contains(body, "go generate") {
		t.Errorf("no binaries: %d %s", code, body)
	}
}
