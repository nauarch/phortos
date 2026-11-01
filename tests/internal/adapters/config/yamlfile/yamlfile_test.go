// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package yamlfile_test

// Verifies:
//   - A file reads as the arguments it stands for, in its order: values as written, a list as
//     one argument per item, empty values and an empty file as nothing
//   - What is not a mapping of settings, a nested value, a key that is no setting (or is config
//     itself) and broken YAML are invalid, saying where; a missing file is not found
//   - Rendered arguments read back as the same arguments, a repeated name as a list, values
//     YAML would misread quoted, under the comment given

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/adapters/config/yamlfile"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAFileReadsAsItsArguments(t *testing.T) {
	got, err := yamlfile.Parse([]byte(`
# the cache
max-size: 100G
release-within: 10s
debug: true
intercept:
  - download.docker.com
  - apt.grafana.com   # alloy
hosts:
resolver: ""
`))
	want := []string{"--max-size=100G", "--release-within=10s", "--debug=true", "--intercept=download.docker.com", "--intercept=apt.grafana.com", "--resolver="}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("%q %v", got, err)
	}
	if got, err := yamlfile.Parse([]byte("# nothing yet\n")); err != nil || len(got) != 0 {
		t.Errorf("empty: %q %v", got, err)
	}
}

func TestWhatIsNotSettingsIsInvalid(t *testing.T) {
	for _, bad := range []string{
		"- a\n- b\n",
		"intercept:\n  host: a\n",
		"intercept:\n  - [a, b]\n",
		"--max-size: 1G\n",
		"config: /etc/other.yaml\n",
		"max-size: 1G\nmax-size: 2G\n",
		"max-size: [\n",
	} {
		if _, err := yamlfile.Parse([]byte(bad)); errkind.Of(err) != errkind.Invalid {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := yamlfile.Parse([]byte("a: 1\nintercept:\n  host: a\n")); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the line is not said: %v", err)
	}
	if _, err := (yamlfile.File{Path: filepath.Join(t.TempDir(), "none.yaml")}).Args(); errkind.Of(err) != errkind.NotFound {
		t.Errorf("missing file: %v", err)
	}
}

func TestRenderedArgumentsReadBack(t *testing.T) {
	args := []string{"--max-size=100G", "--intercept=download.docker.com", "--intercept=apt.grafana.com", "--resolver=192.0.2.53:53", "--debug", "--hosts=*.archive.ubuntu.com", "--release-ttl=true"}
	b, err := yamlfile.Codec{}.Render(args, "phortos' settings\nsecond line")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, "# phortos' settings\n# second line\n") || !strings.Contains(text, "max-size: 100G\n") || !strings.Contains(text, "intercept:\n  - download.docker.com\n") {
		t.Errorf("\n%s", text)
	}
	path := filepath.Join(t.TempDir(), "phortos.yaml")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := yamlfile.File{Path: path}.Args()
	want := []string{"--max-size=100G", "--intercept=download.docker.com", "--intercept=apt.grafana.com", "--resolver=192.0.2.53:53", "--debug=true", "--hosts=*.archive.ubuntu.com", "--release-ttl=true"}
	if err != nil || !slices.Equal(back, want) {
		t.Errorf("%q %v\n%s", back, err, text)
	}
	if _, err := (yamlfile.Codec{}).Render([]string{"max-size=1G"}, ""); errkind.Of(err) != errkind.Invalid {
		t.Errorf("an argument without dashes: %v", err)
	}
}
