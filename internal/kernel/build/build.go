// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package build names the version a binary was built as.
package build

import "runtime/debug"

// Version is stamped when a release sets it (-ldflags "-X main.version=v1.2.3"); otherwise the
// commit Go recorded the build from ("dev-1a2b3c4d", "-dirty" with changes not committed), or
// "dev" when it recorded none.
func Version(stamped string) string {
	if stamped != "" && stamped != "dev" {
		return stamped
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return Dev(info.Settings)
}

// Dev is the version of a build no release stamped, named by the VCS settings Go records.
func Dev(settings []debug.BuildSetting) string {
	var rev string
	var dirty bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	v := "dev-" + rev[:min(8, len(rev))]
	if dirty {
		v += "-dirty"
	}
	return v
}

// Home is where phortos is published.
const Home = "https://github.com/nauarch/phortos"

// About names a binary with its version, where it comes from and its licence, as --version shows
// it: "phortosctl v1.2.3 (https://github.com/nauarch/phortos, Apache-2.0)".
func About(binary, version string) string {
	return binary + " " + version + " (" + Home + ", Apache-2.0)"
}
