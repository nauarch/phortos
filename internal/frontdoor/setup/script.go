// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"fmt"
	"strings"
)

// Script is the setup script for the phortos whose API is at api and proxy at server: it fetches
// the phortosctl built for the host over the same pinned connection, checks it against sums
// (SHA-256 by binary name), and runs it with the arguments the script was given (plan when
// none), adding phortos' addresses and pin to plan and configure. configure installs phortosctl
// for good; the downloaded copy is removed.
func Script(api, server, pin string, sums map[string]string) []byte {
	var cases strings.Builder
	for _, p := range platforms(sums) {
		fmt.Fprintf(&cases, "  %s) sum=%s ;;\n", p, sums[prefix+p])
	}
	return []byte(fmt.Sprintf(`#!/bin/sh
# phortos setup: installs phortosctl from the phortos at %[1]s and points apt at it.
#   %[4]s
# Only curl is used, so every byte comes over the connection pinned to phortos' identity.
# phortos: https://github.com/nauarch/phortos, Copyright 2026 darki73, Apache-2.0.
set -eu
api='%[1]s'
server='%[2]s'
pin='%[3]s'
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "phortos setup: no phortosctl for $(uname -m)" >&2; exit 1 ;;
esac
platform="$(uname -s | tr '[:upper:]' '[:lower:]')-$arch"
case "$platform" in
%[5]s  *) echo "phortos setup: this phortos carries no phortosctl for $platform" >&2; exit 1 ;;
esac
[ "$(id -u)" = 0 ] || { echo "phortos setup: run it as root (| sudo sh)" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "phortos setup: curl is needed, to check phortos' pin" >&2; exit 1; }
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
curl -fsSL -k --pinnedpubkey "$pin" -o "$dir/phortosctl" "https://$api/get/phortosctl-$platform"
echo "$sum  $dir/phortosctl" | sha256sum -c - >/dev/null 2>&1 || { echo "phortos setup: the downloaded phortosctl does not match its checksum" >&2; exit 1; }
chmod 755 "$dir/phortosctl"
[ $# -gt 0 ] || set -- plan
command=$1
shift
case "$command" in
  plan|configure) set -- "$command" --server "$server" --api "$api" --pin "$pin" "$@" ;;
  *) set -- "$command" "$@" ;;
esac
"$dir/phortosctl" "$@"
`, api, server, pin, Command(api, pin), cases.String()))
}
