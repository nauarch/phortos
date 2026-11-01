// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - No program file names InsecureSkipVerify: phortos checks every upstream certificate in full,
//     since it vouches for what it fetches over HTTPS; only tests may skip a check
//   - The one exception, phortosctl's pinned connection to phortos' API, replaces the chain check
//     with a pin check: a file on the allow-list that sets no VerifyConnection is caught too

import (
	"go/ast"
	"testing"
)

// pinned are the files allowed to skip the chain check, each because it checks a pinned key in
// VerifyConnection instead.
var pinned = map[string]string{
	"internal/adapters/phortos/client/trust.go": "phortosctl's connection to phortos' API, pinned to the identity",
}

// insecureProblems lists every program file naming InsecureSkipVerify, with its line, unless it
// is allowed and checks a pin in VerifyConnection.
func insecureProblems(t *testing.T, tr tree, allowed map[string]string) []string {
	var problems []string
	for _, f := range tr.parseGo(t, tr.paths(isSourceGo)) {
		var skips []string
		verifies := false
		ast.Inspect(f.file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				switch id.Name {
				case "InsecureSkipVerify":
					skips = append(skips, f.fset.Position(id.Pos()).String())
				case "VerifyConnection":
					verifies = true
				}
			}
			return true
		})
		if len(skips) == 0 {
			continue
		}
		if _, ok := allowed[f.path]; ok && verifies {
			continue
		}
		for _, s := range skips {
			problems = append(problems, s+": names InsecureSkipVerify")
		}
	}
	return problems
}

func TestNoCertificateCheckIsSkipped(t *testing.T) {
	expectNone(t, insecureProblems(t, repo(t), pinned))
}

func TestAPlantedSkippedCheckIsCaught(t *testing.T) {
	planted := tree{
		"go.mod": []byte(goModFor),
		"internal/adapters/repo/upstream/upstream.go": []byte("package upstream\n\nimport \"crypto/tls\"\n\n" +
			"// C skips.\nvar C = &tls.Config{InsecureSkipVerify: true}\n"),
		"internal/adapters/phortos/client/trust.go": []byte("package client\n\nimport \"crypto/tls\"\n\n" +
			"// C forgot its pin.\nvar C = &tls.Config{InsecureSkipVerify: true}\n"),
		"internal/adapters/phortos/client/ok.go": []byte("package client\n\nimport \"crypto/tls\"\n\n" +
			"// C pins.\nvar C = &tls.Config{InsecureSkipVerify: true, VerifyConnection: nil}\n"),
		"tests/internal/x/x_test.go": []byte("package x\n\nimport \"crypto/tls\"\n\nvar c = &tls.Config{InsecureSkipVerify: true}\n"),
	}
	allowed := map[string]string{"internal/adapters/phortos/client/trust.go": "", "internal/adapters/phortos/client/ok.go": ""}
	expectCaught(t, insecureProblems(t, planted, allowed), "upstream.go:6", "trust.go:6")
}
