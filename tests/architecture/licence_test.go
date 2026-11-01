// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package architecture_test

// Verifies:
//   - Every Go file opens with a copyright line naming its author and the licence by its SPDX
//     name, which Apache-2.0 asks anyone redistributing the source to keep
//   - The repository carries the LICENSE and the NOTICE that name phortos' author

import (
	"bytes"
	"regexp"
	"testing"
)

// licenceHeader opens every Go file: a line per author holding copyright in it, then the
// licence by its SPDX name.
var licenceHeader = regexp.MustCompile(`^(// Copyright \d{4}(-\d{4})? \S[^\n]*\n)+// SPDX-License-Identifier: Apache-2\.0\n`)

// licenceProblems lists every Go file not opening with the header, and a missing LICENSE or
// NOTICE.
func licenceProblems(tr tree) []string {
	var problems []string
	for _, p := range tr.paths(isGo) {
		if !licenceHeader.Match(bytes.ReplaceAll(tr[p], []byte("\r\n"), []byte("\n"))) {
			problems = append(problems, p+": does not open with the licence header")
		}
	}
	if !bytes.Contains(tr["LICENSE"], []byte("Copyright 2026 darki73")) {
		problems = append(problems, "LICENSE: no copyright line")
	}
	if !bytes.Contains(tr["NOTICE"], []byte("darki73")) {
		problems = append(problems, "NOTICE: missing, or does not name the author")
	}
	return problems
}

func TestEveryFileCarriesTheLicence(t *testing.T) {
	expectNone(t, licenceProblems(repo(t)))
}

func TestAPlantedMissingHeaderIsCaught(t *testing.T) {
	const spdx = "// SPDX-License-Identifier: Apache-2.0\n"
	planted := tree{
		"a/a.go": []byte("// Copyright 2026 darki73\n" + spdx + "\n// Package a does a.\npackage a\n"),
		"b/b.go": []byte("// Copyright 2027 Jane Doe\n" + spdx + "\n// Package b does b.\npackage b\n"),
		"c/c.go": []byte("// Package c does c.\npackage c\n"),
		"d/d.go": []byte("// Copyright 2026\n" + spdx + "\n// Package d does d.\npackage d\n"),
		"e/e.go": []byte("// Copyright 2026 darki73\n// SPDX-License-Identifier: MIT\n\n// Package e does e.\npackage e\n"),
		"f/f.go": []byte("// Copyright 2026 darki73\n// Copyright 2027 Jane Doe\n" + spdx + "\n// Package f does f.\npackage f\n"),
	}
	problems := licenceProblems(planted)
	expectCaught(t, problems, "c/c.go: does not open", "d/d.go: does not open", "e/e.go: does not open", "LICENSE: no copyright", "NOTICE: missing")
	for _, p := range problems {
		if bytes.HasPrefix([]byte(p), []byte("a/a.go")) || bytes.HasPrefix([]byte(p), []byte("b/b.go")) || bytes.HasPrefix([]byte(p), []byte("f/f.go")) {
			t.Errorf("a file with a valid header was flagged: %s", p)
		}
	}
}
