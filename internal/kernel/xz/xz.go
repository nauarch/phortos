// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package xz decompresses the .xz format (LZMA2 inside the xz container), which Go's standard
// library lacks and Debian uses for some of its indexes. Only what xz writes for plain data is
// read: LZMA2 as the single filter, any check (none, CRC32, CRC64, SHA-256), several blocks and
// several streams. Every check is verified; anything else is invalid.
package xz

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"

	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// MaxDict bounds the dictionary a stream may ask for; xz's own presets stay at 64 MiB.
const MaxDict = 64 << 20

// magic opens every stream; footerMagic closes it.
var (
	magic       = []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}
	footerMagic = []byte{'Y', 'Z'}
	crc64Table  = crc64.MakeTable(crc64.ECMA)
)

// The check types a stream's flags name.
const (
	checkNone   = 0x00
	checkCRC32  = 0x01
	checkCRC64  = 0x04
	checkSHA256 = 0x0A
)

// lzma2Filter is the filter ID of LZMA2.
const lzma2Filter = 0x21

// Decode writes the decompressed contents of the .xz data in src to dst.
func Decode(dst io.Writer, src io.Reader) error {
	r := bufio.NewReaderSize(src, 64<<10)
	for first := true; ; first = false {
		if !first {
			more, err := skipPadding(r)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		}
		if err := stream(dst, r); err != nil {
			return err
		}
	}
}

// invalid is a malformed-data failure.
func invalid(format string, a ...any) error {
	return fmt.Errorf("xz: "+format+": %w", append(a, errkind.ErrInvalid)...)
}

// skipPadding reads the zero bytes between streams (a multiple of four); false at the end.
func skipPadding(r *bufio.Reader) (bool, error) {
	n := 0
	for {
		b, err := r.ReadByte()
		if errors.Is(err, io.EOF) {
			if n%4 != 0 {
				return false, invalid("stream padding of %d bytes", n)
			}
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if b != 0 {
			if n%4 != 0 {
				return false, invalid("stream padding of %d bytes", n)
			}
			return true, r.UnreadByte()
		}
		n++
	}
}

// stream reads one stream: header, blocks, index and footer.
func stream(dst io.Writer, r *bufio.Reader) error {
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return invalid("stream header: %v", err)
	}
	if !bytes.Equal(head[:6], magic) {
		return invalid("not xz data")
	}
	if crc32.ChecksumIEEE(head[6:8]) != binary.LittleEndian.Uint32(head[8:]) {
		return invalid("stream header checksum")
	}
	if head[6] != 0 || head[7]&0xF0 != 0 {
		return invalid("unsupported stream flags %x", head[6:8])
	}
	check := head[7]
	var blocks []record
	for {
		size, err := r.ReadByte()
		if err != nil {
			return invalid("block header: %v", err)
		}
		if size == 0 {
			break
		}
		rec, err := block(dst, r, size, check)
		if err != nil {
			return err
		}
		blocks = append(blocks, rec)
	}
	indexSize, err := index(r, blocks)
	if err != nil {
		return err
	}
	var foot [12]byte
	if _, err := io.ReadFull(r, foot[:]); err != nil {
		return invalid("stream footer: %v", err)
	}
	if !bytes.Equal(foot[10:], footerMagic) || !bytes.Equal(foot[8:10], head[6:8]) {
		return invalid("stream footer does not match its header")
	}
	if crc32.ChecksumIEEE(foot[4:10]) != binary.LittleEndian.Uint32(foot[:4]) {
		return invalid("stream footer checksum")
	}
	if backward := (int64(binary.LittleEndian.Uint32(foot[4:8])) + 1) * 4; backward != indexSize {
		return invalid("footer says the index has %d bytes, it has %d", backward, indexSize)
	}
	return nil
}

// record is what the index says of a block: its unpadded and uncompressed sizes.
type record struct {
	unpadded, uncompressed int64
}

// block reads one block whose header size byte was size: its header, its LZMA2 data into dst,
// its padding and its check.
func block(dst io.Writer, r *bufio.Reader, size byte, check byte) (record, error) {
	hdr := make([]byte, (int(size)+1)*4)
	hdr[0] = size
	if _, err := io.ReadFull(r, hdr[1:]); err != nil {
		return record{}, invalid("block header: %v", err)
	}
	body, sum := hdr[:len(hdr)-4], binary.LittleEndian.Uint32(hdr[len(hdr)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return record{}, invalid("block header checksum")
	}
	flags := body[1]
	if flags&0x3C != 0 {
		return record{}, invalid("reserved block flags %x", flags)
	}
	if flags&0x03 != 0 {
		return record{}, invalid("only LZMA2 alone is supported, the block has %d filters", flags&0x03+1)
	}
	p := bytes.NewReader(body[2:])
	declaredComp, declaredUncomp := int64(-1), int64(-1)
	if flags&0x40 != 0 {
		v, err := vli(p)
		if err != nil || v == 0 {
			return record{}, invalid("block compressed size")
		}
		declaredComp = int64(v)
	}
	if flags&0x80 != 0 {
		v, err := vli(p)
		if err != nil {
			return record{}, invalid("block uncompressed size")
		}
		declaredUncomp = int64(v)
	}
	id, err := vli(p)
	if err != nil || id != lzma2Filter {
		return record{}, invalid("only LZMA2 is supported, the block uses filter %#x", id)
	}
	n, err := vli(p)
	if err != nil || n != 1 {
		return record{}, invalid("LZMA2 properties of %d bytes", n)
	}
	prop, err := p.ReadByte()
	if err != nil {
		return record{}, invalid("LZMA2 properties")
	}
	dict, err := dictSize(prop)
	if err != nil {
		return record{}, err
	}
	for p.Len() > 0 {
		if b, _ := p.ReadByte(); b != 0 {
			return record{}, invalid("block header padding")
		}
	}

	h := checkHash(check)
	counted := &countReader{r: r}
	out := &countWriter{w: dst}
	var w io.Writer = out
	if h != nil {
		w = io.MultiWriter(out, h)
	}
	if err := lzma2(w, counted, dict); err != nil {
		return record{}, err
	}
	if declaredComp >= 0 && counted.n != declaredComp {
		return record{}, invalid("block holds %d compressed bytes, its header says %d", counted.n, declaredComp)
	}
	if declaredUncomp >= 0 && out.n != declaredUncomp {
		return record{}, invalid("block holds %d bytes, its header says %d", out.n, declaredUncomp)
	}
	for pad := (4 - counted.n%4) % 4; pad > 0; pad-- {
		if b, err := r.ReadByte(); err != nil || b != 0 {
			return record{}, invalid("block padding")
		}
	}
	size2 := checkSize(check)
	if size2 < 0 {
		return record{}, invalid("unsupported check type %#x", check)
	}
	want := make([]byte, size2)
	if _, err := io.ReadFull(r, want); err != nil {
		return record{}, invalid("block check: %v", err)
	}
	if h != nil && !bytes.Equal(checkBytes(check, h), want) {
		return record{}, invalid("block check does not match its data")
	}
	return record{unpadded: int64(len(hdr)) + counted.n + int64(size2), uncompressed: out.n}, nil
}

// index reads the stream's index (its indicator byte already read) and checks it against the
// blocks read; it gives the index's size.
func index(r *bufio.Reader, blocks []record) (int64, error) {
	h := crc32.NewIEEE()
	h.Write([]byte{0})
	tr := io.TeeReader(r, h)
	br := &byteReader{r: tr}
	count, err := vli(br)
	if err != nil || count != uint64(len(blocks)) {
		return 0, invalid("index lists %d blocks, the stream has %d", count, len(blocks))
	}
	for _, b := range blocks {
		unpadded, err1 := vli(br)
		uncomp, err2 := vli(br)
		if err1 != nil || err2 != nil || int64(unpadded) != b.unpadded || int64(uncomp) != b.uncompressed {
			return 0, invalid("index does not match the blocks")
		}
	}
	size := 1 + br.n
	for ; size%4 != 0; size++ {
		if b, err := br.ReadByte(); err != nil || b != 0 {
			return 0, invalid("index padding")
		}
	}
	var sum [4]byte
	if _, err := io.ReadFull(r, sum[:]); err != nil {
		return 0, invalid("index checksum: %v", err)
	}
	if h.Sum32() != binary.LittleEndian.Uint32(sum[:]) {
		return 0, invalid("index checksum")
	}
	return size + 4, nil
}

// vli reads a variable-length integer of at most nine bytes.
func vli(r io.ByteReader) (uint64, error) {
	var v uint64
	for i := range 9 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			if b == 0 && i > 0 {
				return 0, invalid("integer not in its shortest form")
			}
			return v, nil
		}
	}
	return 0, invalid("integer longer than nine bytes")
}

// dictSize is the dictionary size an LZMA2 properties byte names.
func dictSize(prop byte) (int, error) {
	if prop > 40 {
		return 0, invalid("LZMA2 dictionary property %d", prop)
	}
	if prop == 40 {
		return 0, invalid("LZMA2 dictionary larger than %d bytes", MaxDict)
	}
	size := (2 | int64(prop&1)) << (prop/2 + 11)
	if size > MaxDict {
		return 0, invalid("LZMA2 dictionary of %d bytes, more than %d", size, MaxDict)
	}
	return int(size), nil
}

// checkHash is the hash a check type uses, nil for none.
func checkHash(check byte) hash.Hash {
	switch check {
	case checkCRC32:
		return crc32.NewIEEE()
	case checkCRC64:
		return crc64.New(crc64Table)
	case checkSHA256:
		return sha256.New()
	}
	return nil
}

// checkSize is how many bytes a check type stores, -1 for one this package does not know.
func checkSize(check byte) int {
	switch check {
	case checkNone:
		return 0
	case checkCRC32:
		return 4
	case checkCRC64:
		return 8
	case checkSHA256:
		return 32
	}
	return -1
}

// checkBytes is a finished check as the stream stores it: CRCs little-endian, SHA-256 as is.
func checkBytes(check byte, h hash.Hash) []byte {
	sum := h.Sum(nil)
	if check == checkSHA256 {
		return sum
	}
	for i, j := 0, len(sum)-1; i < j; i, j = i+1, j-1 {
		sum[i], sum[j] = sum[j], sum[i]
	}
	return sum
}

// countReader counts the bytes read through it.
type countReader struct {
	r *bufio.Reader
	n int64
}

// Read reads and counts.
func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// ReadByte reads one byte and counts it.
func (c *countReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// countWriter counts the bytes written through it.
type countWriter struct {
	w io.Writer
	n int64
}

// Write writes and counts.
func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// byteReader reads bytes one at a time from a reader and counts them.
type byteReader struct {
	r io.Reader
	n int64
}

// ReadByte reads one byte.
func (b *byteReader) ReadByte() (byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(b.r, one[:]); err != nil {
		return 0, err
	}
	b.n++
	return one[0], nil
}
