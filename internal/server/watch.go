// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// The self-check's patience: how many unanswered checks in a row end phortos, and the longest a
// single check waits.
const (
	selfCheckMisses  = 3
	selfCheckTimeout = 10 * time.Second
)

// Watch asks phortos' own /healthz every interval, through the address clients use, and returns
// once it has gone unanswered selfCheckMisses times in a row: phortos then exits, and its service
// manager (systemd's Restart=, Docker's restart policy) starts it afresh. Any answer counts as
// alive, an unhealthy one too, as that is phortos reporting a fault a restart would not mend;
// each answer is passed on to alive (for systemd's watchdog, which also ends a phortos frozen too
// hard to check itself). It returns nil when ctx ends.
func Watch(ctx context.Context, addr net.Addr, every time.Duration, alive func(), log *slog.Logger) error {
	url := "http://" + loopback(addr) + "/healthz"
	client := &http.Client{Timeout: min(every, selfCheckTimeout)}
	t := time.NewTicker(every)
	defer t.Stop()
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		err := ask(ctx, client, url)
		if err == nil {
			missed = 0
			alive()
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		missed++
		log.Warn("self-check unanswered", "missed", missed, "of", selfCheckMisses, "err", err)
		if missed >= selfCheckMisses {
			return fmt.Errorf("phortos left its own /healthz unanswered %d times in a row: %w", missed, err)
		}
	}
}

// ask sends one check; any answer is nil, no answer the error.
func ask(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// loopback is the listening address as this host reaches it: an unspecified IP (listening on
// all) becomes the loopback one.
func loopback(addr net.Addr) string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return addr.String()
	}
	ip := tcp.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}
	return net.JoinHostPort(ip.String(), fmt.Sprint(tcp.Port))
}
