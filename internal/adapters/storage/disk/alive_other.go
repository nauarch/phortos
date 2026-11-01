// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

//go:build !(linux || darwin || freebsd)

package disk

// processAlive cannot tell on this system, so it says alive: a lock is then taken over only once
// stale, never early.
func processAlive(int) bool { return true }
