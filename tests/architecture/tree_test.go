// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Shared helpers: finds the repository root and walks its files, skipping map/, Git's own
//     folder, build output and binary files
//   - The walk finds this very file and nothing under map/

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// tree is a set of files by slash path relative to the repository root.
type tree map[string][]byte

// skippedDirs are never walked: map/ is internal bookkeeping, the rest is not source.
var skippedDirs = []string{"map", ".git", ".claude", "bin", "dist"}

// repoRoot finds the folder holding go.mod, from the test's working folder upwards.
func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's folder")
		}
		dir = parent
	}
}

var (
	realOnce sync.Once
	realTree tree
	realErr  error
)

// repo is the whole repository as a tree, read once per test run.
func repo(t testing.TB) tree {
	t.Helper()
	root := repoRoot(t)
	realOnce.Do(func() { realTree, realErr = readTree(root) })
	if realErr != nil {
		t.Fatal(realErr)
	}
	return realTree
}

// readTree reads every text file under root outside the skipped folders.
func readTree(root string) (tree, error) {
	out := tree{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if slices.Contains(skippedDirs, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		out[rel] = data
		return nil
	})
	return out, err
}

// paths lists the tree's paths, sorted, that pass keep.
func (tr tree) paths(keep func(string) bool) []string {
	var out []string
	for p := range tr {
		if keep(p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// pathDir is a slash path's folder, "" at the root.
func pathDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

// pathBase is a slash path's last element.
func pathBase(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func isGo(p string) bool     { return strings.HasSuffix(p, ".go") }
func isTestGo(p string) bool { return strings.HasSuffix(p, "_test.go") }

// isSourceGo is a hand-written Go file of the program, outside tests/.
func isSourceGo(p string) bool { return isGo(p) && !isTestGo(p) && !strings.HasPrefix(p, "tests/") }

// modulePath is the module line of the tree's go.mod.
func (tr tree) modulePath(t testing.TB) string {
	t.Helper()
	for line := range strings.SplitSeq(string(tr["go.mod"]), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// parsed is one parsed Go file.
type parsed struct {
	path string
	fset *token.FileSet
	file *ast.File
}

// parseGo parses the Go files among paths, with comments; a file that does not parse fails t.
func (tr tree) parseGo(t testing.TB, paths []string) []parsed {
	t.Helper()
	out := make([]parsed, 0, len(paths))
	for _, p := range paths {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, tr[p], parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, parsed{path: p, fset: fset, file: f})
	}
	return out
}

// imports lists a file's import paths.
func imports(f *ast.File) []string {
	out := make([]string, 0, len(f.Imports))
	for _, s := range f.Imports {
		out = append(out, strings.Trim(s.Path.Value, `"`))
	}
	return out
}

// isStd reports whether an import path belongs to the standard library.
func isStd(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// expectNone fails t with every problem found.
func expectNone(t *testing.T, problems []string) {
	t.Helper()
	for _, p := range problems {
		t.Error(p)
	}
}

// expectCaught fails t unless every wanted problem is among those found, by substring, and
// nothing else was found.
func expectCaught(t *testing.T, problems []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, w) }) {
			t.Errorf("planted violation %q not caught; found %q", w, problems)
		}
	}
	for _, p := range problems {
		if !slices.ContainsFunc(want, func(w string) bool { return strings.Contains(p, w) }) {
			t.Errorf("caught %q, which was not planted", p)
		}
	}
}

// goModFor is a go.mod for planted trees.
const goModFor = "module example.com/m\n\ngo 1.27\n"

func TestTheWalkSeesTheRepositoryButNotTheMap(t *testing.T) {
	tr := repo(t)
	if _, ok := tr["tests/architecture/tree_test.go"]; !ok {
		t.Fatal("the walk misses tests/architecture/tree_test.go")
	}
	for p := range tr {
		if strings.HasPrefix(p, "map/") || strings.HasPrefix(p, ".git/") {
			t.Errorf("the walk reads %s", p)
		}
	}
	if got := tr.modulePath(t); got != "github.com/nauarch/phortos" {
		t.Errorf("module path %q", got)
	}
}
