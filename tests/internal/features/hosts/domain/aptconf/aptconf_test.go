// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package aptconf_test

// Verifies:
//   - The configuration asks detect for http and https, sends Ubuntu Pro's ESM repositories DIRECT
//     without asking it, checks each intercepted host (sorted, once) against the bundle, and
//     refreshes before every update without ever failing it
//   - The detect wrapper hands its arguments to phortosctl detect
//   - Proxy settings of the host's own are conflicts, a DIRECT of the admin's too; phortosctl's own
//     detect and DIRECT lines and empty values are not

import (
	"slices"
	"strings"
	"testing"

	"github.com/nauarch/phortos/internal/features/hosts/domain/aptconf"
	"github.com/nauarch/phortos/internal/features/hosts/domain/layout"
)

func TestTheConfigurationAsksDetectAndChecksInterceptedHosts(t *testing.T) {
	got := string(aptconf.Render(layout.Linux(), []string{"download.docker.com", "developer.download.nvidia.com", "download.docker.com"}))
	want := `// Written by phortosctl; ` + "`phortosctl unconfigure`" + ` removes it.
Acquire::http::Proxy-Auto-Detect "/usr/local/lib/phortos/detect";
Acquire::https::Proxy-Auto-Detect "/usr/local/lib/phortos/detect";
Acquire::http::Proxy::esm.ubuntu.com "DIRECT";
Acquire::https::Proxy::esm.ubuntu.com "DIRECT";
Acquire::https::developer.download.nvidia.com::CaInfo "/usr/local/lib/phortos/ca-bundle.pem";
Acquire::https::download.docker.com::CaInfo "/usr/local/lib/phortos/ca-bundle.pem";
APT::Update::Pre-Invoke { "if [ -x /usr/local/lib/phortos/phortosctl ]; then /usr/local/lib/phortos/phortosctl refresh || true; fi"; };
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(string(aptconf.Render(layout.Linux(), nil)), "CaInfo") {
		t.Error("CaInfo without intercepted hosts")
	}
}

func TestTheDetectWrapperHandsOverItsArguments(t *testing.T) {
	got := string(aptconf.DetectScript(layout.Linux()))
	if !strings.HasPrefix(got, "#!/bin/sh\n") || !strings.Contains(got, `exec /usr/local/lib/phortos/phortosctl detect "$@"`) {
		t.Error(got)
	}
}

func TestOwnProxySettingsConflict(t *testing.T) {
	dump := `APT "";
Acquire::http::Proxy "http://squid.lan:3128";
Acquire::https::Proxy::download.docker.com "DIRECT";
Acquire::http::Proxy-Auto-Detect "/usr/local/lib/phortos/detect";
Acquire::http::Proxy::esm.ubuntu.com "DIRECT";
Acquire::https::Proxy::esm.ubuntu.com "DIRECT";
Acquire::https::Proxy-Auto-Detect "/usr/bin/auto-apt-proxy";
Acquire::http::Proxy "";
Acquire::Retries "3";
`
	got := aptconf.Conflicts(dump, layout.Linux())
	want := []string{
		"Acquire::http::Proxy http://squid.lan:3128",
		"Acquire::https::Proxy::download.docker.com DIRECT",
		"Acquire::https::Proxy-Auto-Detect /usr/bin/auto-apt-proxy",
	}
	if !slices.Equal(got, want) {
		t.Errorf("%q", got)
	}
}
