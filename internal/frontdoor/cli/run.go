// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package cli is phortosctl's command line: it reads a command and its flags, runs it through
// the hosts feature, and prints what happened.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"

	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/kernel/build"
)

// usage is the help printed for no command or an unknown one.
const usage = `phortosctl points a Debian or Ubuntu host's apt at phortos, a caching proxy for apt.

Commands:
  plan         read the host and phortos and report what configure would do (changes nothing)
  configure    point apt at phortos: a dry run that only writes with --yes
  unconfigure  take everything phortosctl wrote off the host (only lists it without --yes)
  verify       run apt-get update and check it went through phortos without warnings
  refresh      renew the working CAs from phortos and install the phortosctl it hands out, unless
               configured with --no-auto-upgrade (apt runs this before every update)
  upgrade      install the phortosctl phortos hands out, when this one differs (a dry run without --yes)
  detect URL   print phortos' proxy address for URL, or DIRECT (apt runs this per host)
  version      print phortosctl's version

Run phortosctl <command> -h for a command's flags.
`

// Options are what the command line drives, the version it reports, and where it prints.
type Options struct {
	Manager hostplan.Manager
	Version string
	Out     io.Writer
	Err     io.Writer
}

// Run runs one command and gives its exit code: 0 done, 1 failed, 2 misused.
func Run(ctx context.Context, args []string, o Options) int {
	if len(args) == 0 {
		fmt.Fprint(o.Err, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "plan":
		err = plan(ctx, args[1:], o)
	case "configure":
		err = configure(ctx, args[1:], o)
	case "unconfigure":
		err = unconfigure(ctx, args[1:], o)
	case "verify":
		err = verify(ctx, args[1:], o)
	case "refresh":
		err = refresh(ctx, args[1:], o)
	case "upgrade":
		err = upgrade(ctx, args[1:], o)
	case "detect":
		detect(ctx, args[1:], o)
		return 0
	case "version", "--version":
		fmt.Fprintln(o.Out, build.About("phortosctl", o.Version))
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(o.Out, usage)
		return 0
	default:
		fmt.Fprintf(o.Err, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp), errors.Is(err, errUsage):
		return 2
	default:
		fmt.Fprintln(o.Err, "phortosctl:", err)
		return 1
	}
}

// errUsage is a command line that cannot run; its flags or the command have said why.
var errUsage = errors.New("usage")

// flags is a command's flag set, printing to o.Err.
func flags(name string, o Options) *flag.FlagSet {
	set := flag.NewFlagSet("phortosctl "+name, flag.ContinueOnError)
	set.SetOutput(o.Err)
	return set
}

// parse reads a command's flags; flags it cannot read are misuse, and the set has said why.
func parse(set *flag.FlagSet, args []string) error {
	if err := set.Parse(args); err != nil {
		return errUsage
	}
	return nil
}

// targetFlags adds the flags naming phortos, and gives the target once parsed: the API defaults
// to the server's host on port 3143 when a pin is given.
func targetFlags(set *flag.FlagSet) func() (hostplan.Target, error) {
	var t hostplan.Target
	set.StringVar(&t.Server, "server", "", "phortos' proxy address, host:port (required)")
	set.StringVar(&t.API, "api", "", "phortos' API address, host:port (default the server's host, port 3143)")
	set.StringVar(&t.Pin, "pin", "", "the pin of phortos' identity, sha256//…, which phortos prints at start; without it no HTTPS repository goes through phortos")
	set.BoolVar(&t.NoAutoUpgrade, "no-auto-upgrade", false, "keep refresh from installing the phortosctl phortos hands out; `phortosctl upgrade --yes` still does")
	return func() (hostplan.Target, error) {
		if t.Server == "" {
			fmt.Fprintln(set.Output(), "phortosctl: --server is required")
			return t, errUsage
		}
		if t.Pin != "" && t.API == "" {
			if host, _, err := net.SplitHostPort(t.Server); err == nil {
				t.API = net.JoinHostPort(host, "3143")
			}
		}
		return t, nil
	}
}
