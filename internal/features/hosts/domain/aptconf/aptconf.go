// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package aptconf writes phortosctl's apt configuration and reads the host's for settings that
// would fight it. apt asks the detect program, per host and per run, whether to go through
// phortos; the update hook refreshes the CA bundle before every update; each intercepted host
// checks its certificates against that bundle.
package aptconf

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
)

// Direct are the hosts apt reaches straight without asking detect: Ubuntu Pro's ESM repositories,
// which need the host's Pro credentials anyway, and whose cache job runs apt under an AppArmor
// profile that may not run detect (it would fail every refresh of the ESM list with error 100).
// apt asks no detect program for a host that has a proxy setting of its own.
var Direct = []string{"esm.ubuntu.com"}

// Render is the apt configuration for the layout and the intercepted hosts (plain names).
func Render(l layout.Layout, hosts []string) []byte {
	var b strings.Builder
	b.WriteString("// Written by phortosctl; `phortosctl unconfigure` removes it.\n")
	fmt.Fprintf(&b, "Acquire::http::Proxy-Auto-Detect %q;\n", l.Detect)
	fmt.Fprintf(&b, "Acquire::https::Proxy-Auto-Detect %q;\n", l.Detect)
	for _, h := range Direct {
		fmt.Fprintf(&b, "Acquire::http::Proxy::%s \"DIRECT\";\n", h)
		fmt.Fprintf(&b, "Acquire::https::Proxy::%s \"DIRECT\";\n", h)
	}
	for _, h := range sorted(hosts) {
		fmt.Fprintf(&b, "Acquire::https::%s::CaInfo %q;\n", h, l.Bundle)
	}
	fmt.Fprintf(&b, "APT::Update::Pre-Invoke { %q; };\n", Hook(l))
	return []byte(b.String())
}

// Hook is the command apt runs before every update: refresh the bundle, never failing the update.
func Hook(l layout.Layout) string {
	return fmt.Sprintf("if [ -x %s ]; then %s refresh || true; fi", l.Binary, l.Binary)
}

// DetectScript is the program apt runs to choose a proxy: phortosctl detect, which prints a
// proxy or DIRECT.
func DetectScript(l layout.Layout) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\n# Written by phortosctl: tells apt whether to go through phortos.\nexec %s detect \"$@\"\n", l.Binary))
}

// Conflicts lists the proxy settings in an `apt-config dump` that are not phortosctl's: another
// proxy or detect program would decide instead of phortos, or fight it. phortosctl's own detect
// and DIRECT lines are not.
func Conflicts(dump string, l layout.Layout) []string {
	var out []string
	for line := range strings.SplitSeq(dump, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSuffix(value, ";"), `"`)
		lower := strings.ToLower(key)
		if !strings.HasPrefix(lower, "acquire::http::proxy") && !strings.HasPrefix(lower, "acquire::https::proxy") {
			continue
		}
		if value == "" || (strings.HasSuffix(lower, "::proxy-auto-detect") && value == l.Detect) || ownDirect(lower, value) {
			continue
		}
		out = append(out, key+" "+value)
	}
	return out
}

// ownDirect reports whether a proxy setting (its key in lower case) is one of phortosctl's DIRECT
// lines.
func ownDirect(key, value string) bool {
	_, host, ok := strings.Cut(key, "::proxy::")
	return ok && value == "DIRECT" && slices.Contains(Direct, host)
}

// sorted is a sorted copy without duplicates.
func sorted(hosts []string) []string {
	out := slices.Clone(hosts)
	slices.Sort(out)
	return slices.Compact(out)
}
