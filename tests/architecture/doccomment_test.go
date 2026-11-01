// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Every Go file carries a comment stating its single job: its first comment comes before its
//     first declaration other than its imports and says something (three words at least)
//   - No comment names history: comments describe the code as it is

import (
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// historyPhrases describe what code was rather than what it is.
var historyPhrases = []string{
	"used to be", "previously", "formerly", "legacy", "the old way", "was renamed", "we changed",
	"in the old",
}

// docProblems lists every Go file without a leading comment and every comment that talks history.
func docProblems(t *testing.T, tr tree) []string {
	var problems []string
	for _, f := range tr.parseGo(t, tr.paths(isGo)) {
		if !leadingComment(f.file) {
			problems = append(problems, f.path+": no comment before its first declaration")
		}
		for _, g := range f.file.Comments {
			text := strings.ToLower(g.Text())
			for _, h := range historyPhrases {
				if strings.Contains(text, h) {
					problems = append(problems, f.path+":"+strconv.Itoa(f.fset.Position(g.Pos()).Line)+": comment names history ("+h+")")
				}
			}
		}
	}
	return problems
}

// leadingComment reports whether the file's first comment, of three words or more, comes before
// its first declaration other than imports; directives such as //go:build are not comments, and
// the licence header says nothing of the file's job.
func leadingComment(f *ast.File) bool {
	i := slices.IndexFunc(f.Comments, func(g *ast.CommentGroup) bool {
		return g.Text() != "" && !strings.Contains(g.Text(), "SPDX-License-Identifier")
	})
	if i < 0 {
		return false
	}
	first := f.Comments[i]
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			continue
		}
		if first.Pos() > d.Pos() {
			return false
		}
		break
	}
	return len(strings.Fields(first.Text())) >= 3
}

func TestEveryGoFileSaysItsJob(t *testing.T) {
	expectNone(t, docProblems(t, repo(t)))
}

func TestAPlantedMissingOrHistoricCommentIsCaught(t *testing.T) {
	planted := tree{
		"a/a.go": []byte("package a\n\nfunc A() {}\n\n// B does b.\nfunc B() {}\n"),
		"b/b.go": []byte("package b\n\n// B used to be something else.\nfunc B() {}\n"),
		"c/c.go": []byte("package c\n\n// C.\nfunc C() {}\n"),
		"d/d.go": []byte("package d\n\n// D says what it does.\nfunc D() {}\n"),
		"e/e.go": []byte("package e\n\nimport \"fmt\"\n\n// E prints a new line.\nfunc E() { fmt.Println() }\n"),
		"f/f.go": []byte("//go:build linux\n\npackage f\n\n// F runs on Linux alone.\nfunc F() {}\n"),
		"g/g.go": []byte("//go:build linux\n\npackage g\n\nfunc G() {}\n"),
		"h/h.go": []byte("// Copyright 2026 someone\n// SPDX-License-Identifier: Apache-2.0\n\npackage h\n\nfunc H() {}\n"),
	}
	expectCaught(t, docProblems(t, planted),
		"a/a.go: no comment before",
		"b/b.go:3: comment names history (used to be)",
		"c/c.go: no comment before",
		"g/g.go: no comment before",
		"h/h.go: no comment before",
	)
}
