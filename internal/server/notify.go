// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net"
	"os"
	"strings"
)

// Notify tells systemd how phortos is doing (READY=1, WATCHDOG=1) through the socket systemd names
// in NOTIFY_SOCKET; without one (not under systemd, or not asked to) it does nothing.
func Notify(state string) error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write([]byte(state))
	return err
}
