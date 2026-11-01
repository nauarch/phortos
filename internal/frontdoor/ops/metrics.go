// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package ops

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// metric is one Prometheus series: its name, help, type, labels (as `key="value"`, or "") and
// value.
type metric struct {
	name, help, kind, labels string
	value                    int64
}

// metrics answers the counters in Prometheus' text format.
func (d *door) metrics(w http.ResponseWriter, _ *http.Request) {
	s := d.o.Reporter.Stats()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	write(w, []metric{
		{"phortos_build_info", "The version phortos was built as.", "gauge", `version="` + label.Replace(d.o.Version) + `"`, 1},
		{"phortos_file_requests_total", "Requests for index and package files by how they were served.", "counter", `result="hit"`, s.Hits},
		{"phortos_file_requests_total", "", "", `result="joined"`, s.Joins},
		{"phortos_file_requests_total", "", "", `result="miss"`, s.Misses},
		{"phortos_file_requests_total", "", "", `result="passed"`, s.Passed},
		{"phortos_download_failures_total", "Downloads from upstream that failed, stalled or did not match their sum.", "counter", "", s.Failures},
		{"phortos_release_requests_total", "Release file requests by how they were answered.", "counter", `result="fresh"`, s.ReleasesFresh},
		{"phortos_release_requests_total", "", "", `result="checked"`, s.ReleasesChecked},
		{"phortos_release_requests_total", "", "", `result="changed"`, s.ReleasesChanged},
		{"phortos_release_requests_total", "", "", `result="stale"`, s.ReleasesStale},
		{"phortos_packages_indexes_read_total", "Packages indexes read and checked, by outcome.", "counter", `result="read"`, s.PackagesRead},
		{"phortos_packages_indexes_read_total", "", "", `result="failed"`, s.PackagesReadFailed},
		{"phortos_prefetched_files_total", "Downloads started ahead of a request a client had queued behind others.", "counter", "", s.Prefetched},
		{"phortos_served_bytes_total", "Bytes sent to clients from the cache.", "counter", "", s.BytesServed},
		{"phortos_fetched_bytes_total", "Bytes fetched from upstream to be kept.", "counter", "", s.BytesFetched},
		{"phortos_passed_bytes_total", "Bytes passed through uncached.", "counter", "", s.BytesPassed},
		{"phortos_evicted_files_total", "Files evicted to keep the store under its cap.", "counter", "", s.Evicted},
		{"phortos_evicted_bytes_total", "Bytes evicted to keep the store under its cap.", "counter", "", s.EvictedBytes},
		{"phortos_store_bytes", "The store's size at the last measurement.", "gauge", "", s.StoreBytes},
		{"phortos_store_files", "The store's file count at the last measurement.", "gauge", "", s.StoreFiles},
		{"phortos_store_max_bytes", "The store's cap; 0 when uncapped.", "gauge", "", s.MaxBytes},
	})
}

// label escapes a label value as Prometheus' text format wants it.
var label = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// write prints series, with HELP and TYPE before the first of each name.
func write(w io.Writer, series []metric) {
	for _, m := range series {
		if m.help != "" {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", m.name, m.help, m.name, m.kind)
		}
		if m.labels != "" {
			fmt.Fprintf(w, "%s{%s} %d\n", m.name, m.labels, m.value)
		} else {
			fmt.Fprintf(w, "%s %d\n", m.name, m.value)
		}
	}
}
