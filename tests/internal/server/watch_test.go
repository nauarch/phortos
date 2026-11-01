// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package server_test

// Verifies:
//   - A phortos that accepts connections but never answers is given up on after three checks
//   - Any answer, an unhealthy one too, counts as alive; Watch ends quietly with its context
//   - Listening on all addresses is checked through the loopback one

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nauarch/phortos/internal/server"
)

// quiet logs nowhere.
var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestAPhortosThatNeverAnswersIsGivenUpOn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	start := time.Now()
	err = server.Watch(context.Background(), ln.Addr(), 50*time.Millisecond, func() {}, quiet)
	if err == nil || !strings.Contains(err.Error(), "unanswered 3 times") || time.Since(start) > 5*time.Second {
		t.Errorf("%v after %s", err, time.Since(start))
	}
}

func TestAnyAnswerCountsAsAlive(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "store: read-only", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := server.Watch(ctx, s.Listener.Addr(), 50*time.Millisecond, func() {}, quiet); err != nil {
		t.Errorf("%v", err)
	}
}

func TestListeningOnAllIsCheckedOnLoopback(t *testing.T) {
	asked := make(chan string, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case asked <- r.URL.Path:
		default:
		}
	})}
	go s.Serve(ln)
	defer s.Close()
	all := &net.TCPAddr{IP: net.IPv6unspecified, Port: ln.Addr().(*net.TCPAddr).Port}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := server.Watch(ctx, all, 50*time.Millisecond, func() {}, quiet); err != nil {
		t.Fatal(err)
	}
	if got := <-asked; got != "/healthz" {
		t.Errorf("asked %s", got)
	}
}
