// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

//go:build ignore

// build.go builds phortosctl for every platform phortos hands it out for, into bin/, for dist to
// embed, stamped with PHORTOS_VERSION when it is set. go generate runs it from this folder.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// platforms are the ones phortosctl is built for: apt hosts, on x86 and ARM.
var platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}

func main() {
	for _, p := range platforms {
		out, err := filepath.Abs(filepath.Join("bin", fmt.Sprintf("phortosctl-%s-%s", p[0], p[1])))
		if err != nil {
			fail(err)
		}
		ldflags := "-s -w"
		if v := os.Getenv("PHORTOS_VERSION"); v != "" {
			ldflags += " -X main.version=" + v
		}
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", out, "github.com/nauarch/phortos/cmd/phortosctl")
		cmd.Env = append(os.Environ(), "GOOS="+p[0], "GOARCH="+p[1], "CGO_ENABLED=0")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fail(err)
		}
		fmt.Println("built", out)
	}
}

// fail ends the build with why.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "building phortosctl:", err)
	os.Exit(1)
}
