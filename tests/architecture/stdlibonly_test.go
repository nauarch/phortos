// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Packages under internal/kernel import the standard library and the kernel only
//   - Packages under internal/contracts import the standard library, the kernel and contracts only
//   - Every feature's domain/ imports the standard library, the kernel, contracts and its own
//     feature's domain only

import (
	"strings"
	"testing"
)

// stdlibProblems lists every import a pure package may not make.
func stdlibProblems(t *testing.T, tr tree) []string {
	mod := tr.modulePath(t)
	kernel, contracts := mod+"/internal/kernel", mod+"/internal/contracts"
	under := func(imp, root string) bool { return imp == root || strings.HasPrefix(imp, root+"/") }
	var problems []string
	for _, f := range tr.parseGo(t, tr.paths(isSourceGo)) {
		var allowed func(string) bool
		switch domain := domainRoot(f.path); {
		case strings.HasPrefix(f.path, "internal/kernel/"):
			allowed = func(imp string) bool { return under(imp, kernel) }
		case strings.HasPrefix(f.path, "internal/contracts/"):
			allowed = func(imp string) bool { return under(imp, kernel) || under(imp, contracts) }
		case domain != "":
			allowed = func(imp string) bool {
				return under(imp, kernel) || under(imp, contracts) || under(imp, mod+"/"+domain)
			}
		default:
			continue
		}
		for _, imp := range imports(f.file) {
			if !isStd(imp) && !allowed(imp) {
				problems = append(problems, f.path+": imports "+imp)
			}
		}
	}
	return problems
}

// domainRoot is, for a file in a feature's domain, that domain folder; "" for any other file.
func domainRoot(path string) string {
	rest, ok := strings.CutPrefix(path, "internal/features/")
	if !ok {
		return ""
	}
	parts := strings.Split(pathDir(rest), "/")
	for i, part := range parts {
		if part == "domain" {
			return "internal/features/" + strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

func TestPurePackagesImportOnlyTheStandardLibraryAndTheKernel(t *testing.T) {
	expectNone(t, stdlibProblems(t, repo(t)))
}

func TestAPlantedImpureImportIsCaught(t *testing.T) {
	planted := tree{
		"go.mod": []byte(goModFor),
		"internal/kernel/digest/digest.go": []byte("package digest\n\n// Digest digests.\nimport (\n\t\"crypto/sha256\"\n" +
			"\t\"example.com/m/internal/kernel/errkind\"\n\t\"example.com/m/internal/contracts/pull\"\n)\n"),
		"internal/contracts/pull/pull.go": []byte("package pull\n\n// Pull pulls.\nimport (\n" +
			"\t\"example.com/m/internal/contracts/storage\"\n\t\"example.com/m/internal/adapters/storage/disk\"\n)\n"),
		"internal/features/cache/domain/policy/stale.go": []byte("package policy\n\n// Stale decides.\nimport (\n" +
			"\t\"example.com/m/internal/features/cache/domain/lru\"\n\t\"example.com/m/internal/features/cache/app/fetch\"\n\t\"net/http\"\n)\n"),
		"internal/features/cache/app/fetch/blobs.go": []byte("package fetch\n\n// Blobs fetch.\nimport \"example.com/m/internal/adapters/storage/disk\"\n"),
	}
	expectCaught(t, stdlibProblems(t, planted),
		"digest.go: imports example.com/m/internal/contracts/pull",
		"pull.go: imports example.com/m/internal/adapters/storage/disk",
		"stale.go: imports example.com/m/internal/features/cache/app/fetch",
	)
}
