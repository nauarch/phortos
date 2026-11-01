// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Every _test.go under tests/ (outside tests/architecture and tests/kit) mirrors a source file:
//     tests/X/Y_test.go needs X/Y.go, and its package is the source package's name plus _test
//   - Every source package under internal/ with logic (a function with a body) has at least one
//     mirrored test; cmd/ only wires and is left out
//   - Only tests/kit may hold non-test Go files under tests/

import (
	"go/ast"
	"strings"
	"testing"
)

// unmirrored are the test folders that mirror nothing.
var unmirrored = []string{"tests/architecture/", "tests/kit/"}

func isUnmirrored(p string) bool {
	for _, u := range unmirrored {
		if strings.HasPrefix(p, u) {
			return true
		}
	}
	return false
}

// mirrorProblems lists every test without a source, every misnamed test package, every stray
// non-test file under tests/, and every package with logic but no test.
func mirrorProblems(t *testing.T, tr tree) []string {
	var problems []string
	pkgOf := map[string]string{}
	logic := map[string]bool{}
	for _, f := range tr.parseGo(t, tr.paths(isSourceGo)) {
		dir := pathDir(f.path)
		pkgOf[dir] = f.file.Name.Name
		if strings.HasPrefix(f.path, "internal/") && hasLogic(f.file) {
			logic[dir] = true
		}
	}
	tested := map[string]bool{}
	for _, f := range tr.parseGo(t, tr.paths(func(p string) bool { return strings.HasPrefix(p, "tests/") && isGo(p) })) {
		switch {
		case !isTestGo(f.path):
			if !strings.HasPrefix(f.path, "tests/kit/") {
				problems = append(problems, f.path+": only tests/kit may hold Go files that are not tests")
			}
		case isUnmirrored(f.path):
		default:
			src := strings.TrimSuffix(strings.TrimPrefix(f.path, "tests/"), "_test.go") + ".go"
			if _, ok := tr[src]; !ok {
				problems = append(problems, f.path+": mirrors no source file "+src)
				continue
			}
			if want := pkgOf[pathDir(src)] + "_test"; f.file.Name.Name != want {
				problems = append(problems, f.path+": package "+f.file.Name.Name+", want "+want)
			}
			tested[pathDir(src)] = true
		}
	}
	for dir := range logic {
		if !tested[dir] {
			problems = append(problems, dir+": has logic but no mirrored test under tests/"+dir)
		}
	}
	return problems
}

// hasLogic reports whether a file declares a function or method with a body.
func hasLogic(f *ast.File) bool {
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			return true
		}
	}
	return false
}

func TestEveryTestMirrorsASource(t *testing.T) {
	expectNone(t, mirrorProblems(t, repo(t)))
}

func TestAPlantedMirrorViolationIsCaught(t *testing.T) {
	planted := tree{
		"internal/a/a.go":            []byte("package a\n\n// A does a.\nfunc A() {}\n"),
		"internal/b/b.go":            []byte("package b\n\n// B is a type only.\ntype B int\n"),
		"internal/c/c.go":            []byte("package c\n\n// C does c.\nfunc C() {}\n"),
		"tests/internal/c/c_test.go": []byte("package wrong_test\n"),
		"tests/internal/d/d_test.go": []byte("package d_test\n"),
		"tests/internal/c/helper.go": []byte("package c_test\n"),
		"tests/kit/fake/fake.go":     []byte("package fake\n"),
	}
	expectCaught(t, mirrorProblems(t, planted),
		"internal/a: has logic but no mirrored test",
		"tests/internal/c/c_test.go: package wrong_test, want c_test",
		"tests/internal/d/d_test.go: mirrors no source file internal/d/d.go",
		"tests/internal/c/helper.go: only tests/kit may hold",
	)
}
