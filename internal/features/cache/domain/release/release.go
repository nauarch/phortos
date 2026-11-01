// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package release reads an apt release file (InRelease or Release): the SHA256 and size of every
// index file it lists, or its SHA512 where the release lists no SHA256 for it (NVIDIA's container
// toolkit lists SHA512 only). The signature is apt's to check; phortos only learns the sums,
// which decide what it may keep.
package release

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Sum is a file's SHA256 (64 lower-case hex digits) and size; with SHA512, Hex is the file's
// SHA512 instead (128 digits), for a file a release lists by no SHA256.
type Sum struct {
	Hex    string
	Size   int64
	SHA512 bool
}

// Release is what a release file lists: index files by their path relative to the release
// file's folder, and whether the repository serves them by hash too.
type Release struct {
	Files  map[string]Sum
	ByHash bool
}

// Parse reads a release file, signed inline (InRelease) or not (Release). A file's SHA256 is
// taken when listed, its SHA512 otherwise. A release listing neither is invalid: nothing in it
// could be verified.
func Parse(b []byte) (Release, error) {
	body := unsign(b)
	r := Release{Files: map[string]Sum{}}
	strong := map[string]Sum{}
	var section string
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			var err error
			switch section {
			case "sha256":
				err = addSum(r.Files, line, false)
			case "sha512":
				err = addSum(strong, line, true)
			}
			if err != nil {
				return Release{}, err
			}
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		section = key
		if key == "acquire-by-hash" {
			r.ByHash = strings.EqualFold(value, "yes")
		}
	}
	if err := sc.Err(); err != nil {
		return Release{}, fmt.Errorf("release file: %w: %w", errkind.ErrInvalid, err)
	}
	for p, s := range strong {
		if _, ok := r.Files[p]; !ok {
			r.Files[p] = s
		}
	}
	if len(r.Files) == 0 {
		return Release{}, fmt.Errorf("release file lists no SHA256 or SHA512 sums: %w", errkind.ErrInvalid)
	}
	return r, nil
}

// addSum reads one " <sum> <size> <path>" line of the SHA256 section, or of the SHA512 one. A
// path climbing out of the release file's folder is invalid.
func addSum(files map[string]Sum, line string, sha512 bool) error {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return fmt.Errorf("release file: sum line %q: %w", line, errkind.ErrInvalid)
	}
	newSum := NewSum
	if sha512 {
		newSum = NewSHA512
	}
	sum, err := newSum(fields[0], fields[1])
	if err != nil {
		return err
	}
	p := path.Clean(fields[2])
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return fmt.Errorf("release file: path %q: %w", fields[2], errkind.ErrInvalid)
	}
	files[p] = sum
	return nil
}

// NewSHA512 reads a SHA512 in hex (128 lower-case digits) and a size in decimal.
func NewSHA512(hex, size string) (Sum, error) {
	if len(hex) != 128 || !IsHex(hex[:64]) || !IsHex(hex[64:]) {
		return Sum{}, fmt.Errorf("sha512 %q: %w", hex, errkind.ErrInvalid)
	}
	s, err := NewSum(hex[:64], size)
	if err != nil {
		return Sum{}, err
	}
	return Sum{Hex: hex, Size: s.Size, SHA512: true}, nil
}

// NewSum reads a SHA256 in hex and a size in decimal.
func NewSum(hex, size string) (Sum, error) {
	if !IsHex(hex) {
		return Sum{}, fmt.Errorf("sha256 %q: %w", hex, errkind.ErrInvalid)
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n < 0 {
		return Sum{}, fmt.Errorf("size %q: %w", size, errkind.ErrInvalid)
	}
	return Sum{Hex: hex, Size: n}, nil
}

// IsHex reports whether s is a SHA256 in 64 lower-case hex digits.
func IsHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// unsign is the signed text of an inline-signed file, with its dash escapes undone; an unsigned
// file comes back as it is.
func unsign(b []byte) []byte {
	const begin, sig = "-----BEGIN PGP SIGNED MESSAGE-----", "-----BEGIN PGP SIGNATURE-----"
	text := bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(bytes.TrimLeft(text, "\n"), []byte(begin)) {
		return text
	}
	_, rest, ok := bytes.Cut(text, []byte("\n\n"))
	if !ok {
		return nil
	}
	if i := bytes.Index(rest, []byte("\n"+sig)); i >= 0 {
		rest = rest[:i+1]
	}
	var out bytes.Buffer
	for line := range bytes.Lines(rest) {
		out.Write(bytes.TrimPrefix(line, []byte("- ")))
	}
	return out.Bytes()
}
