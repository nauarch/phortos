// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package errkind_test

// Verifies:
//   - A wrapped sentinel names its kind, a timeout counts as unreachable, a cancel as canceled
//   - An error wrapping no sentinel is internal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

func TestAWrappedSentinelNamesItsKind(t *testing.T) {
	for err, want := range map[error]errkind.Kind{
		fmt.Errorf("x: %w", errkind.ErrNotFound):    errkind.NotFound,
		fmt.Errorf("x: %w", errkind.ErrInvalid):     errkind.Invalid,
		fmt.Errorf("x: %w", errkind.ErrForbidden):   errkind.Forbidden,
		fmt.Errorf("x: %w", errkind.ErrUpstream):    errkind.Upstream,
		fmt.Errorf("x: %w", errkind.ErrUnreachable): errkind.Unreachable,
		fmt.Errorf("x: %w", context.Canceled):       errkind.Canceled,
		context.DeadlineExceeded:                    errkind.Unreachable,
		errors.New("something else"):                errkind.Internal,
	} {
		if got := errkind.Of(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}
