// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package errkind

// Kind names a class of failure (not found, invalid, forbidden, conflict, upstream, unreachable,
// canceled) that a front door answers with. Internal is every other failure.
type Kind string

// The kinds.
const (
	NotFound    Kind = "not-found"
	Invalid     Kind = "invalid"
	Forbidden   Kind = "forbidden"
	Conflict    Kind = "conflict"
	Upstream    Kind = "upstream"
	Unreachable Kind = "unreachable"
	Canceled    Kind = "canceled"
	Internal    Kind = "internal"
)
