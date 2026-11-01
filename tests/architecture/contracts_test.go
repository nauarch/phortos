// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Packages under internal/contracts declare only interfaces, plain types and constants: no
//     functions, no methods with bodies, no function literals

import (
	"go/ast"
	"strings"
	"testing"
)

// contractProblems lists every function, method or function literal in a contract.
func contractProblems(t *testing.T, tr tree) []string {
	var problems []string
	for _, f := range tr.parseGo(t, tr.paths(func(p string) bool { return isSourceGo(p) && strings.HasPrefix(p, "internal/contracts/") })) {
		ast.Inspect(f.file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				problems = append(problems, f.path+": declares func "+n.Name.Name)
			case *ast.FuncLit:
				problems = append(problems, f.path+": holds a function literal")
			}
			return true
		})
	}
	return problems
}

func TestContractsHoldOnlyTypes(t *testing.T) {
	expectNone(t, contractProblems(t, repo(t)))
}

func TestAPlantedContractFunctionIsCaught(t *testing.T) {
	planted := tree{
		"internal/contracts/pull/pull.go": []byte("package pull\n\n// Puller pulls.\n" +
			"type Puller interface{ Serves(string) bool }\n\n// Name is a name.\ntype Name string\n\n" +
			"// String spells it.\nfunc (n Name) String() string { return string(n) }\n"),
		"internal/contracts/storage/store.go": []byte("package storage\n\n// Open opens.\nvar Open = func() int { return 1 }\n"),
	}
	expectCaught(t, contractProblems(t, planted), "pull.go: declares func String", "store.go: holds a function literal")
}
