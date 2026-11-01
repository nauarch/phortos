// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server_test

// Verifies:
//   - Outside systemd telling it anything does nothing
//   - Under systemd the state reaches the socket it names

import (
	"net"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nauarch/phortos/internal/server"
)

func TestNotifyOutsideSystemdDoesNothing(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := server.Notify("READY=1"); err != nil {
		t.Error(err)
	}
}

func TestNotifyReachesSystemdsSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no datagram unix sockets on windows")
	}
	path := filepath.Join(t.TempDir(), "notify")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	if err := server.Notify("WATCHDOG=1"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "WATCHDOG=1" {
		t.Errorf("%q %v", buf[:n], err)
	}
}
