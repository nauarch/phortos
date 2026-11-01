// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package authconf reads apt's credentials files (auth.conf, auth.conf.d/*, netrc syntax) for the
// hosts they name. Those repositories need credentials, which must never pass through phortos,
// so detect sends them straight. Only the hosts are read; logins and passwords are skipped.
package authconf

import (
	"slices"
	"strings"
)

// Hosts are the hosts an auth.conf names after `machine`, lower case, sorted. A machine may carry
// a scheme, a port or a path (https://repo.example.com:8443/debian); only the host is kept.
func Hosts(data []byte) []string {
	var out []string
	fields := strings.Fields(stripComments(string(data)))
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "machine":
			if i+1 < len(fields) {
				if h := hostOf(fields[i+1]); h != "" {
					out = append(out, h)
				}
			}
			i++
		case "login", "password", "account", "port":
			// Each takes a value, which is never a keyword, whatever it reads.
			i++
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// hostOf is the host of a machine entry.
func hostOf(m string) string {
	if _, rest, ok := strings.Cut(m, "://"); ok {
		m = rest
	}
	m, _, _ = strings.Cut(m, "/")
	if strings.HasPrefix(m, "[") {
		end := strings.Index(m, "]")
		if end < 0 {
			return ""
		}
		return strings.ToLower(m[1:end])
	}
	m, _, _ = strings.Cut(m, ":")
	return strings.ToLower(m)
}

// stripComments drops # comments, which netrc files allow at the start of a line.
func stripComments(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
