// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/contracts/trust"
	"github.com/nauarch/phortos/internal/kernel/errkind"
	"github.com/nauarch/phortos/internal/kernel/seal"
)

// maxBinary bounds a phortosctl binary fetched from phortos.
const maxBinary = 128 << 20

// errPin ends a handshake with a server whose key is not the pinned one.
var errPin = errors.New("the server's key is not the pinned one")

// Trust fetches the bundle from the API, pinned, with the identity's key as presented.
func (c *Client) Trust(ctx context.Context, api, pin string) (trust.Bundle, *ecdsa.PublicKey, error) {
	body, identity, err := c.pinned(ctx, api, pin, "/trust", maxAnswer)
	if err != nil {
		return trust.Bundle{}, nil, err
	}
	var b trust.Bundle
	if err := json.Unmarshal(body, &b); err != nil {
		return trust.Bundle{}, nil, fmt.Errorf("phortos' trust bundle: %w: %w", errkind.ErrUpstream, err)
	}
	return b, identity, nil
}

// Tools fetches the version and the binaries' checksums from the API, pinned.
func (c *Client) Tools(ctx context.Context, api, pin string) (ops.Tools, error) {
	body, _, err := c.pinned(ctx, api, pin, "/get/tools", maxAnswer)
	if err != nil {
		return ops.Tools{}, err
	}
	var t ops.Tools
	if err := json.Unmarshal(body, &t); err != nil {
		return ops.Tools{}, fmt.Errorf("phortos' tools: %w: %w", errkind.ErrUpstream, err)
	}
	return t, nil
}

// Binary fetches a phortosctl binary from the API, pinned.
func (c *Client) Binary(ctx context.Context, api, pin, name string) ([]byte, error) {
	body, _, err := c.pinned(ctx, api, pin, "/get/"+name, maxBinary)
	return body, err
}

// pinned fetches a path from the API over TLS pinned to pin, giving the identity's key as the
// server presented it. The pin replaces the chain check: the identity's certificate is
// self-signed and names no host the client could know, so the only thing worth checking is its
// key, which must hash to pin before any byte is exchanged.
func (c *Client) pinned(ctx context.Context, api, pin, path string, limit int64) ([]byte, *ecdsa.PublicKey, error) {
	var mu sync.Mutex
	var identity *ecdsa.PublicKey
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The chain check is replaced by the pin check in VerifyConnection, which runs on every
		// handshake and fails it unless the key matches.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errPin
			}
			cert := cs.PeerCertificates[0]
			key, ok := cert.PublicKey.(*ecdsa.PublicKey)
			if !ok || seal.Pin(cert.RawSubjectPublicKeyInfo) != pin {
				return errPin
			}
			mu.Lock()
			identity = key
			mu.Unlock()
			return nil
		},
	}
	hc := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: cfg}, Timeout: c.timeout}
	defer hc.CloseIdleConnections()
	body, err := c.get(ctx, hc, "https://"+api+path, limit)
	if err != nil {
		if errors.Is(err, errPin) {
			return nil, nil, fmt.Errorf("%s: %w: %w", api, errPin, errkind.ErrForbidden)
		}
		return nil, nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	if identity == nil {
		return nil, nil, fmt.Errorf("%s: %w: %w", api, errPin, errkind.ErrForbidden)
	}
	return body, identity, nil
}
