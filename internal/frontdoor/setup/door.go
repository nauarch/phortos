// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package setup is phortos' front door for a host's first contact, served on the pinned API: GET
// /setup, a script that installs phortosctl and points apt at phortos, and GET /get/{name}, the
// phortosctl binaries it fetches. The owner runs it with the pin phortos prints at start, so
// curl refuses any server without the identity's key before a byte of the script is read:
//
//	curl -fsSL -k --pinnedpubkey 'sha256//<pin>' https://phortos.lan:3143/setup | sudo sh -s -- configure --yes
package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/nauarch/phortos/internal/contracts/ops"
)

// prefix starts every binary's name: phortosctl-<os>-<arch>.
const prefix = "phortosctl-"

// Options are what the door hands out: the phortosctl binaries (at the top of Files), the pin
// the scripts carry, the port of phortos' proxy (the host is the one the client reached the API
// at), the version phortos was built as, and where it logs.
type Options struct {
	Files     fs.FS
	Pin       string
	ProxyPort string
	Version   string
	Log       *slog.Logger
}

// door serves the setup script and the binaries.
type door struct {
	o    Options
	once sync.Once
	sums map[string]string
}

// New builds the door.
func New(o Options) http.Handler {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	d := &door{o: o}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", d.script)
	mux.HandleFunc("GET /get/tools", d.tools)
	mux.HandleFunc("GET /get/{name}", d.binary)
	return mux
}

// tools answers the version and the binaries' checksums, for phortosctl to tell whether it is
// the one phortos hands out.
func (d *door) tools(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(ops.Tools{Version: d.o.Version, Binaries: d.checksums()}); err != nil {
		d.o.Log.Debug("tools not sent", "err", err)
	}
}

// Command is the one-liner that sets a host up from the API at api (host:port) with pin.
func Command(api, pin string) string {
	return "curl -fsSL -k --pinnedpubkey '" + pin + "' https://" + api + "/setup | sudo sh -s -- configure --yes"
}

// host is what a Host header may hold to be put into a script: a name or address, and a port.
var host = regexp.MustCompile(`^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)(:[0-9]{1,5})?$`)

// script answers the setup script for phortos as the asking host reached it.
func (d *door) script(w http.ResponseWriter, r *http.Request) {
	if !host.MatchString(r.Host) {
		http.Error(w, "phortos setup: the Host header is not a plain host:port", http.StatusBadRequest)
		return
	}
	sums := d.checksums()
	if len(sums) == 0 {
		http.Error(w, "phortos setup: this phortos carries no phortosctl (built without go generate)", http.StatusServiceUnavailable)
		return
	}
	api := r.Host
	name := api
	if h, _, err := net.SplitHostPort(api); err == nil {
		name = h
	} else {
		api = net.JoinHostPort(api, "443")
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(Script(api, net.JoinHostPort(strings.Trim(name, "[]"), d.o.ProxyPort), d.o.Pin, sums))
}

// binary answers one phortosctl binary.
func (d *door) binary(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := d.checksums()[name]; !ok {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(d.o.Files, name)
	if err != nil {
		d.o.Log.Error("reading a binary to hand out", "name", name, "err", err)
		http.Error(w, "phortos setup: cannot read "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

// checksums are the SHA-256 of each phortosctl binary, by name, worked out once.
func (d *door) checksums() map[string]string {
	d.once.Do(func() {
		d.sums = map[string]string{}
		if d.o.Files == nil {
			return
		}
		entries, err := fs.ReadDir(d.o.Files, ".")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			data, err := fs.ReadFile(d.o.Files, e.Name())
			if err != nil {
				continue
			}
			sum := sha256.Sum256(data)
			d.sums[e.Name()] = hex.EncodeToString(sum[:])
		}
	})
	return d.sums
}

// platforms lists the binaries' platforms (os-arch), sorted.
func platforms(sums map[string]string) []string {
	var out []string
	for name := range sums {
		out = append(out, strings.TrimPrefix(name, prefix))
	}
	slices.Sort(out)
	return out
}
