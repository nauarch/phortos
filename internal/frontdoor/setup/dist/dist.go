// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package dist carries the phortosctl binaries phortos hands out at /get, built into it at
// compile time. `go generate ./internal/frontdoor/setup/dist` builds them into bin/ first; a
// phortos built without them serves no phortosctl and says so.
package dist

import "embed"

//go:generate go run build.go

// Files are the binaries, under bin/, named phortosctl-<os>-<arch>.
//
//go:embed bin
var Files embed.FS
