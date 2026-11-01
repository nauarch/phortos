// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - phortos depends on nothing outside the standard library but the modules allowed, each
//     imported only by the one folder allowed to: go.mod requires no other module, and every Go
//     file imports only the standard library, phortos itself and what its folder is allowed

import (
	"path"
	"strings"
	"testing"
)

// dependencies are the modules phortos may use, each with the one folder allowed to import it:
// YAML (MIT and Apache-2.0) for the config file.
var dependencies = map[string]string{
	"go.yaml.in/yaml/v3": "internal/adapters/config/yamlfile",
}

// required lists the modules go.mod requires, in both of its forms ("require m v" and a
// "require (" block).
func required(gomod []byte) []string {
	var mods []string
	block := false
	for line := range strings.SplitSeq(string(gomod), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 0:
		case block && fields[0] == ")":
			block = false
		case block:
			mods = append(mods, fields[0])
		case fields[0] == "require" && len(fields) > 1 && fields[1] == "(":
			block = true
		case fields[0] == "require" && len(fields) > 1:
			mods = append(mods, fields[1])
		}
	}
	return mods
}

// dependencyProblems lists every required module not allowed, and every import from outside the
// standard library and the module that its file's folder is not allowed.
func dependencyProblems(t *testing.T, tr tree, allowed map[string]string) []string {
	mod := tr.modulePath(t)
	var problems []string
	for _, m := range required(tr["go.mod"]) {
		if _, ok := allowed[m]; !ok {
			problems = append(problems, "go.mod: requires "+m)
		}
	}
	for _, f := range tr.parseGo(t, tr.paths(isGo)) {
		for _, imp := range imports(f.file) {
			if isStd(imp) || imp == mod || strings.HasPrefix(imp, mod+"/") {
				continue
			}
			if dir, ok := allowed[imp]; ok && path.Dir(f.path) == dir {
				continue
			}
			problems = append(problems, f.path+": imports "+imp)
		}
	}
	return problems
}

func TestNothingOutsideTheStandardLibraryIsUsedButWhatIsAllowed(t *testing.T) {
	expectNone(t, dependencyProblems(t, repo(t), dependencies))
}

func TestAPlantedDependencyIsCaught(t *testing.T) {
	planted := tree{
		"go.mod":              []byte(goModFor + "\nrequire github.com/x/y v1.0.0\n\nrequire (\n\tgithub.com/ok/z v1.0.0 // indirect\n\tgithub.com/x/w v1.0.0\n)\n"),
		"a/a.go":              []byte("package a\n\n// A uses y.\nimport \"github.com/x/y\"\n"),
		"b/b.go":              []byte("package b\n\n// B uses its own.\nimport (\n\t\"net/http\"\n\t\"example.com/m/internal/c\"\n)\n"),
		"internal/ok/ok.go":   []byte("package ok\n\n// Ok may use z.\nimport \"github.com/ok/z\"\n"),
		"internal/other/o.go": []byte("package other\n\n// Other may not.\nimport \"github.com/ok/z\"\n"),
		"go.sum":              nil,
		"README":              nil,
		"x/x.txt":             nil,
	}
	got := dependencyProblems(t, planted, map[string]string{"github.com/ok/z": "internal/ok"})
	expectCaught(t, got, "go.mod: requires github.com/x/y", "go.mod: requires github.com/x/w", "a/a.go: imports github.com/x/y", "internal/other/o.go: imports github.com/ok/z")
	for _, p := range got {
		if strings.Contains(p, "ok.go") || p == "go.mod: requires github.com/ok/z" {
			t.Errorf("an allowed dependency was flagged: %s", p)
		}
	}
}
