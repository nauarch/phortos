// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

//go:build linux || darwin || freebsd

package disk

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process runs on this host: signal 0 checks without sending,
// and a process of another user refuses (EPERM) yet runs.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
