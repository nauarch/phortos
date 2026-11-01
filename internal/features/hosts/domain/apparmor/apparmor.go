// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package apparmor writes what Ubuntu Pro's ESM cache job needs to run phortosctl's detect
// program. The job runs apt under the ubuntu_pro_esm_cache profile, which may run nothing it does
// not name, so apt's Proxy-Auto-Detect fails there (error 100) and the job's ESM list is never
// refreshed. The profile includes a file of local additions; phortosctl keeps a marked block in
// it that runs detect under a profile of its own, allowed only what detect does (run the shell
// and phortosctl, read the state, ask phortos), so the job's own confinement stays as it was.
//
// By default phortosctl writes no block: apt reaches the ESM repositories DIRECT and never asks
// detect for them (aptconf.Direct), and phortosctl only takes out a block an earlier version
// wrote. The block is kept for sending ESM through phortos on hosts that want it, but as written
// it does not work there: the job runs with no_new_privs, under which AppArmor refuses the change
// to a child profile.
package apparmor

import (
	"bytes"
	"fmt"

	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
)

// The lines phortosctl's block starts and ends with.
const (
	begin = "# phortosctl: begin (written by phortosctl, removed by phortosctl unconfigure)"
	end   = "# phortosctl: end"
)

// Block is phortosctl's block for the layout.
func Block(l layout.Layout) []byte {
	return []byte(fmt.Sprintf(`%s
%s Cx -> phortos_detect,
profile phortos_detect flags=(attach_disconnected) {
  include <abstractions/base>
  include <abstractions/nameservice>
  %s r,
  /{,usr/}bin/{,da}sh mrix,
  %s mrix,
  %s r,
  /sys/kernel/mm/transparent_hugepage/hpage_pmd_size r,
  network inet stream,
  network inet6 stream,
}
%s
`, begin, l.Detect, l.Detect, l.Binary, l.State, end))
}

// With is the local additions with phortosctl's block in them, in place of any block of its own
// already there; everything else stays as it is.
func With(local []byte, l layout.Layout) []byte {
	rest := Without(local)
	if len(rest) > 0 && !bytes.HasSuffix(rest, []byte("\n")) {
		rest = append(rest, '\n')
	}
	return append(rest, Block(l)...)
}

// Without is the local additions with phortosctl's block taken out; everything else stays as it
// is. A block missing its end line is taken out to the end of the file.
func Without(local []byte) []byte {
	i := bytes.Index(local, []byte(begin))
	if i < 0 {
		return bytes.Clone(local)
	}
	out := bytes.Clone(local[:i])
	if j := bytes.Index(local[i:], []byte(end)); j >= 0 {
		after := local[i+j+len(end):]
		out = append(out, bytes.TrimPrefix(after, []byte("\n"))...)
	}
	return out
}
