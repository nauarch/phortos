// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package packages reads an apt Packages index: the file name, SHA256 and size of every package
// it lists, which is what lets phortos verify a package before keeping it.
package packages

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/nauarch/phortos/internal/features/cache/domain/release"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Read streams an uncompressed Packages index, calling found for every stanza that names a file
// with its SHA256 and size. A stanza missing one of them is skipped, as apt could not verify it
// either; a malformed sum fails the index.
func Read(r io.Reader, found func(filename string, sum release.Sum)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var filename, hex, size string
	flush := func() error {
		defer func() { filename, hex, size = "", "", "" }()
		if filename == "" || hex == "" || size == "" {
			return nil
		}
		sum, err := release.NewSum(hex, size)
		if err != nil {
			return fmt.Errorf("packages: %s: %w", filename, err)
		}
		found(filename, sum)
		return nil
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "filename":
			filename = strings.TrimSpace(value)
		case "sha256":
			hex = strings.TrimSpace(value)
		case "size":
			size = strings.TrimSpace(value)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("packages: %w: %w", errkind.ErrInvalid, err)
	}
	return flush()
}
