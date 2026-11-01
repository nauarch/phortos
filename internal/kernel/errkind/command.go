// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package errkind

import (
	"fmt"
	"strings"
)

// Command is a failed command: what ran, then what it printed (left out when it printed nothing),
// as kind, wrapping err.
func Command(what, output string, kind, err error) error {
	if said := strings.TrimSpace(output); said != "" {
		return fmt.Errorf("%s: %s: %w: %w", what, said, kind, err)
	}
	return fmt.Errorf("%s: %w: %w", what, kind, err)
}
