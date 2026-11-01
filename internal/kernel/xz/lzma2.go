// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package xz

import (
	"io"
)

// lzma2 decodes one block's LZMA2 data from r into w, with a dictionary of dict bytes. LZMA2 is
// a sequence of chunks, each uncompressed or LZMA, ended by a zero byte; each LZMA chunk starts a
// fresh range decoder and may reset the state, the properties or the dictionary.
func lzma2(w io.Writer, r io.ByteReader, dict int) error {
	win := newWindow(dict)
	var dec *decoder
	first := true
	for {
		ctrl, err := r.ReadByte()
		if err != nil {
			return invalid("LZMA2 chunk: %v", err)
		}
		switch {
		case ctrl == 0x00:
			return win.flush(w)
		case ctrl == 0x01 || ctrl == 0x02:
			if first && ctrl != 0x01 {
				return invalid("LZMA2 starts without resetting its dictionary")
			}
			if ctrl == 0x01 {
				win.reset()
			}
			size, err := be16(r)
			if err != nil {
				return err
			}
			for range size + 1 {
				b, err := r.ReadByte()
				if err != nil {
					return invalid("LZMA2 uncompressed chunk: %v", err)
				}
				win.put(b)
			}
		case ctrl >= 0x80:
			reset := (ctrl >> 5) & 0x03
			if first && reset != 3 {
				return invalid("LZMA2 starts without resetting its dictionary")
			}
			hi := int(ctrl & 0x1F)
			lo, err := be16(r)
			if err != nil {
				return err
			}
			unpacked := hi<<16 + lo + 1
			packed, err := be16(r)
			if err != nil {
				return err
			}
			if reset == 3 {
				win.reset()
			}
			if reset >= 2 {
				prop, err := r.ReadByte()
				if err != nil {
					return invalid("LZMA2 properties: %v", err)
				}
				lc, lp, pb, err := properties(prop)
				if err != nil {
					return err
				}
				dec = newDecoder(win, lc, lp, pb)
			} else if dec == nil {
				return invalid("LZMA2 chunk without properties")
			} else if reset == 1 {
				dec.reset()
			}
			data := make([]byte, packed+1)
			for i := range data {
				if data[i], err = r.ReadByte(); err != nil {
					return invalid("LZMA2 compressed chunk: %v", err)
				}
			}
			if err := dec.chunk(data, unpacked); err != nil {
				return err
			}
		default:
			return invalid("LZMA2 control byte %#x", ctrl)
		}
		first = false
		if err := win.flush(w); err != nil {
			return err
		}
	}
}

// be16 reads a big-endian 16-bit number.
func be16(r io.ByteReader) (int, error) {
	a, err := r.ReadByte()
	if err != nil {
		return 0, invalid("LZMA2 chunk header: %v", err)
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, invalid("LZMA2 chunk header: %v", err)
	}
	return int(a)<<8 | int(b), nil
}

// properties reads lc, lp and pb from an LZMA properties byte; LZMA2 caps lc+lp at 4.
func properties(p byte) (lc, lp, pb int, err error) {
	if p >= 9*5*5 {
		return 0, 0, 0, invalid("LZMA properties %d", p)
	}
	lc, p2 := int(p%9), int(p/9)
	lp, pb = p2%5, p2/5
	if lc+lp > 4 {
		return 0, 0, 0, invalid("LZMA2 lc+lp of %d", lc+lp)
	}
	return lc, lp, pb, nil
}

// window is the dictionary: the last size bytes written, which matches copy from. Bytes written
// since the last flush wait in out.
type window struct {
	buf   []byte
	pos   int
	full  bool
	total int64
	out   []byte
}

// newWindow is an empty dictionary of size bytes.
func newWindow(size int) *window { return &window{buf: make([]byte, size)} }

// reset empties the dictionary.
func (w *window) reset() { w.pos, w.full = 0, false }

// put appends a byte.
func (w *window) put(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos, w.full = 0, true
	}
	w.total++
	w.out = append(w.out, b)
}

// has reports whether a match dist+1 bytes back lies inside the dictionary.
func (w *window) has(dist uint32) bool {
	return int64(dist) < int64(len(w.buf)) && (w.full || int(dist) < w.pos)
}

// empty reports whether nothing was written since the last reset.
func (w *window) empty() bool { return w.pos == 0 && !w.full }

// back is the byte dist+1 bytes back.
func (w *window) back(dist uint32) byte {
	i := w.pos - int(dist) - 1
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// flush writes what was put since the last flush.
func (w *window) flush(dst io.Writer) error {
	if len(w.out) == 0 {
		return nil
	}
	_, err := dst.Write(w.out)
	w.out = w.out[:0]
	return err
}
