// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package layout is where phortosctl keeps what it installs on a host, and where it reads what
// the host already has. Everything phortosctl writes is a file of its own, but for one: the local
// additions to Ubuntu Pro's AppArmor profile, where it keeps a marked block of its own and leaves
// the rest as it is, so unconfigure only removes.
package layout

// Layout names the paths, all absolute.
type Layout struct {
	// Lib holds the installed phortosctl, the detect wrapper apt runs and the CA bundle apt
	// checks intercepted hosts against.
	Lib    string
	Binary string
	Detect string
	Bundle string
	// Staged is where an upgrade puts the new phortosctl to try it before it replaces Binary;
	// Previous is the phortosctl it replaced, kept to go back to.
	Staged   string
	Previous string
	// State is what configure and refresh learned: phortos' addresses, the pin, the intercepted
	// hosts, the installed CAs, the hosts with credentials. World-readable, since detect runs as
	// apt's unprivileged user; it holds nothing secret. Its folder is phortosctl's alone.
	State string
	// AptConf is phortosctl's apt configuration.
	AptConf string
	// SystemCAs is the host's CA file, the base of the bundle.
	SystemCAs string
	// AuthConf and AuthConfDir are apt's credentials; their hosts are never sent to phortos.
	AuthConf    string
	AuthConfDir string
	// ESMProfile is Ubuntu Pro's AppArmor profile for its ESM cache job, which runs apt confined
	// and so must be let run Detect; ESMLocal is the file of local additions it includes.
	ESMProfile string
	ESMLocal   string
}

// Linux is the layout on a Debian or Ubuntu host.
func Linux() Layout {
	return Layout{
		Lib:         "/usr/local/lib/phortos",
		Binary:      "/usr/local/lib/phortos/phortosctl",
		Detect:      "/usr/local/lib/phortos/detect",
		Bundle:      "/usr/local/lib/phortos/ca-bundle.pem",
		Staged:      "/usr/local/lib/phortos/phortosctl.new",
		Previous:    "/usr/local/lib/phortos/phortosctl.old",
		State:       "/etc/phortos/phortosctl.json",
		AptConf:     "/etc/apt/apt.conf.d/00phortos",
		SystemCAs:   "/etc/ssl/certs/ca-certificates.crt",
		AuthConf:    "/etc/apt/auth.conf",
		AuthConfDir: "/etc/apt/auth.conf.d",
		ESMProfile:  "/etc/apparmor.d/ubuntu_pro_esm_cache",
		ESMLocal:    "/etc/apparmor.d/local/ubuntu_pro_esm_cache",
	}
}
