// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/hostplan"
	"github.com/nauarch/phortos/internal/contracts/ops"
)

// plan reports what configure would do.
func plan(ctx context.Context, args []string, o Options) error {
	set := flags("plan", o)
	target := targetFlags(set)
	if err := parse(set, args); err != nil {
		return err
	}
	t, err := target()
	if err != nil {
		return err
	}
	p, err := o.Manager.Plan(ctx, t)
	if err != nil {
		return err
	}
	report(o, p)
	return nil
}

// configure points apt at phortos, or only says how without --yes.
func configure(ctx context.Context, args []string, o Options) error {
	set := flags("configure", o)
	target := targetFlags(set)
	yes := set.Bool("yes", false, "write the changes; without it this is a dry run")
	if err := parse(set, args); err != nil {
		return err
	}
	t, err := target()
	if err != nil {
		return err
	}
	if !*yes {
		p, err := o.Manager.Plan(ctx, t)
		if err != nil {
			return err
		}
		report(o, p)
		fmt.Fprintln(o.Out, "\nDry run: nothing was written. Run again with --yes to configure.")
		return nil
	}
	p, err := o.Manager.Configure(ctx, t)
	if err != nil {
		return err
	}
	report(o, p)
	fmt.Fprintln(o.Out, "\nConfigured. apt now asks phortos on every run; `phortosctl verify` checks it.")
	return nil
}

// unconfigure removes what configure wrote.
func unconfigure(ctx context.Context, args []string, o Options) error {
	set := flags("unconfigure", o)
	yes := set.Bool("yes", false, "remove the files; without it this only lists them")
	if err := parse(set, args); err != nil {
		return err
	}
	paths, err := o.Manager.Unconfigure(ctx, *yes)
	if err != nil {
		return err
	}
	verb := "Would remove"
	if *yes {
		verb = "Removed"
	}
	fmt.Fprintf(o.Out, "%s:\n", verb)
	for _, p := range paths {
		fmt.Fprintf(o.Out, "  %s\n", p)
	}
	if !*yes {
		fmt.Fprintln(o.Out, "\nDry run: nothing was removed. Run again with --yes.")
	}
	return nil
}

// verify runs an apt update through phortos.
func verify(ctx context.Context, args []string, o Options) error {
	set := flags("verify", o)
	if err := parse(set, args); err != nil {
		return err
	}
	r, err := o.Manager.Verify(ctx)
	fmt.Fprintf(o.Out, "phortos answered %d requests during apt-get update.\n", r.Requests)
	for _, w := range r.Warnings {
		fmt.Fprintf(o.Out, "  apt: %s\n", w)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(o.Out, "OK: apt goes through phortos without warnings.")
	return nil
}

// refresh renews the working CAs; it says something only when something changed or failed, as
// apt prints it on every update.
func refresh(ctx context.Context, args []string, o Options) error {
	set := flags("refresh", o)
	if err := parse(set, args); err != nil {
		return err
	}
	what, err := o.Manager.Refresh(ctx)
	if err != nil {
		return fmt.Errorf("refresh failed, keeping what is installed: %w", err)
	}
	if what != "unchanged" {
		fmt.Fprintf(o.Out, "phortosctl: %s\n", what)
	}
	return nil
}

// upgrade installs the phortosctl phortos hands out, or says what it would do without --yes.
func upgrade(ctx context.Context, args []string, o Options) error {
	set := flags("upgrade", o)
	yes := set.Bool("yes", false, "install it; without it this only says what would happen")
	if err := parse(set, args); err != nil {
		return err
	}
	what, err := o.Manager.Upgrade(ctx, *yes)
	if err != nil {
		return err
	}
	fmt.Fprintln(o.Out, what)
	return nil
}

// detect prints apt's answer for one URL; whatever goes wrong, the answer is DIRECT.
func detect(ctx context.Context, args []string, o Options) {
	if len(args) != 1 {
		fmt.Fprintln(o.Out, ops.Direct)
		return
	}
	fmt.Fprintln(o.Out, o.Manager.Detect(ctx, args[0]))
}

// report prints a plan.
func report(o Options, p hostplan.Plan) {
	w := o.Out
	fmt.Fprintf(w, "phortos:       %s\n", p.Target.Server)
	if p.Target.Pin != "" {
		fmt.Fprintf(w, "API:           %s\n", p.Target.API)
		fmt.Fprintf(w, "pin:           %s\n", p.Target.Pin)
	} else {
		fmt.Fprintln(w, "pin:           none; HTTPS repositories go straight")
	}
	if p.PhortosVersion != "" {
		fmt.Fprintf(w, "phortos is:    %s\n", p.PhortosVersion)
	}
	if p.Outdated {
		fmt.Fprintln(w, "OUTDATED:      this phortosctl is not the one phortos hands out; set the host up with phortos' one-liner instead")
	}
	if p.Target.Pin != "" {
		if p.Target.NoAutoUpgrade {
			fmt.Fprintln(w, "auto-upgrade:  no; `phortosctl upgrade --yes` installs the phortosctl phortos hands out")
		} else {
			fmt.Fprintln(w, "auto-upgrade:  yes; every apt update installs the phortosctl phortos hands out")
		}
	}
	if p.Configured {
		fmt.Fprintln(w, "configured:    yes, before; configure rewrites it")
	}
	fmt.Fprintf(w, "intercepted:   %s\n", orNone(p.Intercepted))
	for _, c := range p.CAs {
		fmt.Fprintf(w, "working CA:    %s, until %s\n", c.Fingerprint, c.NotAfter.UTC().Format(time.DateOnly))
	}
	fmt.Fprintf(w, "credentials:   %s\n", orNone(p.Credentialed))
	if len(p.Credentialed) > 0 {
		fmt.Fprintln(w, "               (these hosts always go straight; phortos never sees credentials)")
	}
	for _, c := range p.Conflicts {
		fmt.Fprintf(w, "CONFLICT:      %s\n", c)
	}
	fmt.Fprintln(w, "writes:")
	for _, f := range p.Writes {
		fmt.Fprintf(w, "  %s\n", f)
	}
}

// orNone lists names, or says none.
func orNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
