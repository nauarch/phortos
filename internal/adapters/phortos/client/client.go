// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package client is phortosctl's side of talking to phortos: the route and the counters from the
// proxy over plain HTTP, and the trust bundle from the API over TLS pinned to the identity.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nauarch/phortos/internal/contracts/host"
	"github.com/nauarch/phortos/internal/contracts/ops"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// maxAnswer bounds what is read from phortos.
const maxAnswer = 4 << 20

// Client talks to phortos. Requests carry no proxy: phortos is asked directly, whatever the
// environment says.
type Client struct {
	plain   *http.Client
	timeout time.Duration
}

var _ host.Phortos = (*Client)(nil)

// New builds a client whose every exchange is bounded by timeout (zero for 10 s); callers' own
// deadlines apply too.
func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{plain: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: timeout}, timeout: timeout}
}

// Route asks the proxy how to reach u.
func (c *Client) Route(ctx context.Context, server, u string) (ops.Route, error) {
	var r ops.Route
	body, err := c.get(ctx, c.plain, "http://"+server+"/api/route?url="+url.QueryEscape(u), maxAnswer)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("phortos' route answer: %w: %w", errkind.ErrUpstream, err)
	}
	return r, nil
}

// Requests adds up the file and release requests phortos' metrics count.
func (c *Client) Requests(ctx context.Context, server string) (int64, error) {
	body, err := c.get(ctx, c.plain, "http://"+server+"/metrics", maxAnswer)
	if err != nil {
		return 0, err
	}
	var total int64
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "phortos_file_requests_total{") && !strings.HasPrefix(line, "phortos_release_requests_total{") {
			continue
		}
		n, err := strconv.ParseInt(line[strings.LastIndexByte(line, ' ')+1:], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("phortos' metrics: %q: %w", line, errkind.ErrUpstream)
		}
		total += n
	}
	return total, nil
}

// get fetches a URL whole, failing on any status but 200 and on a body larger than limit.
func (c *Client) get(ctx context.Context, hc *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", u, errkind.ErrInvalid, err)
	}
	req.Header.Set("User-Agent", "phortosctl")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", u, errkind.ErrUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", u, errkind.ErrUnreachable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s: %w", u, resp.Status, errkind.ErrUpstream)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes: %w", u, limit, errkind.ErrUpstream)
	}
	return body, nil
}
