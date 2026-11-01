// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package errkind

import (
	"context"
	"errors"
)

// sentinels pairs each sentinel with its kind, the most specific first.
var sentinels = []struct {
	err  error
	kind Kind
}{
	{ErrNotFound, NotFound},
	{ErrInvalid, Invalid},
	{ErrForbidden, Forbidden},
	{ErrConflict, Conflict},
	{ErrUnreachable, Unreachable},
	{ErrUpstream, Upstream},
	{context.Canceled, Canceled},
	{context.DeadlineExceeded, Unreachable},
}

// Of finds the Kind an error wraps, the most specific first, and Internal when it wraps none.
func Of(err error) Kind {
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.kind
		}
	}
	return Internal
}
