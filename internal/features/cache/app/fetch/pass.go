// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package fetch

import (
	"context"

	"github.com/nauarch/phortos/internal/contracts/repo"
	"github.com/nauarch/phortos/internal/contracts/upstream"
)

// Pass hands out files phortos cannot verify, straight from upstream and without keeping them:
// whatever the catalog does not know (a package of a repository whose index phortos cannot read,
// a file outside any release) is still served, only never cached.
type Pass struct {
	Upstream upstream.Upstream
	Counters *Counters
}

// Get is upstream's answer for url, asked as the client asked.
func (p *Pass) Get(ctx context.Context, url string, ask repo.Ask) (repo.File, error) {
	resp, err := p.Upstream.Fetch(ctx, url, upstream.Conditions{Modified: ask.Modified})
	if err != nil {
		return repo.File{}, err
	}
	p.Counters.Passed.Add(1)
	return repo.File{
		Status: resp.Status, Size: resp.Size, Modified: resp.Modified, ETag: resp.ETag, ContentType: resp.ContentType,
		Body: counted{ReadCloser: resp.Body, n: &p.Counters.BytesPassed}, Source: repo.Passed,
	}, nil
}
