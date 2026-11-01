// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - A feature imports another feature only through its root package, never its internals
//   - Adapters and front doors import no feature at all: adapters implement contracts, doors serve
//     contracts, and the composition root hands features to them

import (
	"strings"
	"testing"
)

// featureImportProblems lists every import of another feature's internals, and every feature
// import by an adapter or a front door.
func featureImportProblems(t *testing.T, tr tree) []string {
	mod := tr.modulePath(t)
	features := mod + "/internal/features/"
	var problems []string
	for _, f := range tr.parseGo(t, tr.paths(isSourceGo)) {
		own := featureOf(f.path)
		outside := strings.HasPrefix(f.path, "internal/adapters/") || strings.HasPrefix(f.path, "internal/frontdoor/")
		for _, imp := range imports(f.file) {
			rest, ok := strings.CutPrefix(imp, features)
			if !ok {
				continue
			}
			other, _, deep := strings.Cut(rest, "/")
			switch {
			case outside:
				problems = append(problems, f.path+": an adapter or front door imports feature "+other)
			case other != own && deep:
				problems = append(problems, f.path+": imports "+imp+", inside feature "+other+"; use its root package")
			}
		}
	}
	return problems
}

// featureOf is the feature a path under internal/features/ belongs to; "" outside any.
func featureOf(path string) string {
	rest, ok := strings.CutPrefix(path, "internal/features/")
	if !ok {
		return ""
	}
	name, _, deep := strings.Cut(rest, "/")
	if !deep {
		return ""
	}
	return name
}

func TestFeaturesMeetOnlyAtTheirRoots(t *testing.T) {
	expectNone(t, featureImportProblems(t, repo(t)))
}

func TestAPlantedFeatureImportIsCaught(t *testing.T) {
	planted := tree{
		"go.mod": []byte(goModFor),
		"internal/features/hosts/app/migrate/migrate.go": []byte("package migrate\n\n// Migrate migrates.\n" +
			"import (\n\t\"example.com/m/internal/features/cache\"\n\t\"example.com/m/internal/features/cache/app/fetch\"\n" +
			"\t\"example.com/m/internal/features/hosts/domain/plan\"\n)\n"),
		"internal/adapters/storage/disk/dir.go": []byte("package disk\n\n// Dir stores.\nimport \"example.com/m/internal/features/cache\"\n"),
		"internal/frontdoor/mirror/door.go":     []byte("package mirror\n\n// Door serves.\nimport \"example.com/m/internal/features/cache\"\n"),
	}
	expectCaught(t, featureImportProblems(t, planted),
		"migrate.go: imports example.com/m/internal/features/cache/app/fetch",
		"dir.go: an adapter or front door imports feature cache",
		"door.go: an adapter or front door imports feature cache",
	)
}
