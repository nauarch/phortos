// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/nauarch/phortos/internal/kernel/digest"
)

// blobPath is where a file lives: blobs/sha256/ab/abcdef…, fanned out by its first two digits.
func (s *Dir) blobPath(d digest.Digest) string {
	h := d.Hex()
	return filepath.Join(s.root, "blobs", digest.Algorithm, h[:2], h)
}

// releasePath is where a release URL's record lives: releases/ab/<sha256 of the URL>.json. Hashing
// the URL keeps any host, port or path out of file names, which SMB and NFS would each restrict
// differently; the record holds the URL itself.
func (s *Dir) releasePath(url string) string {
	sum := sha256.Sum256([]byte(url))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(s.root, "releases", h[:2], h+".json")
}
