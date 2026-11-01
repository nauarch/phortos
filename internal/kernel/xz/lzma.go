// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package xz

// The LZMA decoder, following the structure of the LZMA specification's reference decoder
// (LzmaSpec.cpp): a range decoder over adaptive bit probabilities, literals coded against the
// previous byte, and matches with lengths, distances and four recent distances.

// LZMA's constants.
const (
	numStates         = 12
	posBitsMax        = 4
	numLenToPosStates = 4
	numAlignBits      = 4
	startPosModel     = 4
	endPosModel       = 14
	numFullDistances  = 1 << (endPosModel >> 1)
	matchMinLen       = 2
	probInit          = 1 << 10
	topValue          = 1 << 24
)

// rangeDecoder reads bits from one LZMA2 chunk's compressed bytes.
type rangeDecoder struct {
	data  []byte
	pos   int
	rng   uint32
	code  uint32
	short bool
}

// init starts decoding data; its first byte must be zero.
func (rd *rangeDecoder) init(data []byte) error {
	if len(data) < 5 || data[0] != 0 {
		return invalid("LZMA range coder start")
	}
	rd.data, rd.pos, rd.rng, rd.code, rd.short = data, 5, 0xFFFFFFFF, 0, false
	for _, b := range data[1:5] {
		rd.code = rd.code<<8 | uint32(b)
	}
	if rd.code == rd.rng {
		return invalid("LZMA range coder start")
	}
	return nil
}

// next is the next compressed byte; reading past the chunk marks it short.
func (rd *rangeDecoder) next() byte {
	if rd.pos >= len(rd.data) {
		rd.short = true
		return 0
	}
	b := rd.data[rd.pos]
	rd.pos++
	return b
}

// normalize keeps the range wide enough.
func (rd *rangeDecoder) normalize() {
	if rd.rng < topValue {
		rd.rng <<= 8
		rd.code = rd.code<<8 | uint32(rd.next())
	}
}

// bit decodes one bit with an adaptive probability.
func (rd *rangeDecoder) bit(p *uint16) uint32 {
	bound := (rd.rng >> 11) * uint32(*p)
	var b uint32
	if rd.code < bound {
		*p += (1<<11 - *p) >> 5
		rd.rng = bound
	} else {
		*p -= *p >> 5
		rd.code -= bound
		rd.rng -= bound
		b = 1
	}
	rd.normalize()
	return b
}

// direct decodes n bits of even probability.
func (rd *rangeDecoder) direct(n int) uint32 {
	var res uint32
	for ; n > 0; n-- {
		rd.rng >>= 1
		rd.code -= rd.rng
		t := 0 - (rd.code >> 31)
		rd.code += rd.rng & t
		rd.normalize()
		res = res<<1 + (t + 1)
	}
	return res
}

// tree decodes n bits through a bit tree, most significant first.
func (rd *rangeDecoder) tree(probs []uint16, n int) uint32 {
	m := uint32(1)
	for range n {
		m = m<<1 + rd.bit(&probs[m])
	}
	return m - (1 << n)
}

// reverse decodes n bits through a bit tree, least significant first.
func (rd *rangeDecoder) reverse(probs []uint16, n int) uint32 {
	m, sym := uint32(1), uint32(0)
	for i := range n {
		b := rd.bit(&probs[m])
		m = m<<1 + b
		sym |= b << i
	}
	return sym
}

// lenDecoder decodes match lengths (0-based, before matchMinLen is added).
type lenDecoder struct {
	choice, choice2 uint16
	low, mid        [1 << posBitsMax][1 << 3]uint16
	high            [1 << 8]uint16
}

// reset sets every probability to even.
func (l *lenDecoder) reset() {
	l.choice, l.choice2 = probInit, probInit
	for i := range l.low {
		fill(l.low[i][:])
		fill(l.mid[i][:])
	}
	fill(l.high[:])
}

// decode is one length.
func (l *lenDecoder) decode(rd *rangeDecoder, posState uint32) uint32 {
	if rd.bit(&l.choice) == 0 {
		return rd.tree(l.low[posState][:], 3)
	}
	if rd.bit(&l.choice2) == 0 {
		return 8 + rd.tree(l.mid[posState][:], 3)
	}
	return 16 + rd.tree(l.high[:], 8)
}

// decoder is LZMA's state across the chunks of one LZMA2 stream.
type decoder struct {
	win        *window
	lc, lp, pb int
	rd         rangeDecoder

	state                  uint32
	rep0, rep1, rep2, rep3 uint32

	literal    []uint16
	isMatch    [numStates << posBitsMax]uint16
	isRep      [numStates]uint16
	isRepG0    [numStates]uint16
	isRepG1    [numStates]uint16
	isRepG2    [numStates]uint16
	isRep0Long [numStates << posBitsMax]uint16
	posSlot    [numLenToPosStates][1 << 6]uint16
	posDecoder [1 + numFullDistances - endPosModel]uint16
	align      [1 << numAlignBits]uint16
	length     lenDecoder
	repLength  lenDecoder
}

// newDecoder is a decoder with properties lc, lp and pb writing into win.
func newDecoder(win *window, lc, lp, pb int) *decoder {
	d := &decoder{win: win, lc: lc, lp: lp, pb: pb, literal: make([]uint16, 0x300<<(lc+lp))}
	d.reset()
	return d
}

// reset starts the state and every probability afresh.
func (d *decoder) reset() {
	d.state, d.rep0, d.rep1, d.rep2, d.rep3 = 0, 0, 0, 0, 0
	fill(d.literal)
	fill(d.isMatch[:])
	fill(d.isRep[:])
	fill(d.isRepG0[:])
	fill(d.isRepG1[:])
	fill(d.isRepG2[:])
	fill(d.isRep0Long[:])
	for i := range d.posSlot {
		fill(d.posSlot[i][:])
	}
	fill(d.posDecoder[:])
	fill(d.align[:])
	d.length.reset()
	d.repLength.reset()
}

// chunk decodes one LZMA2 chunk: data holds its compressed bytes, unpacked how many bytes it
// yields. The chunk must end exactly where its compressed bytes do.
func (d *decoder) chunk(data []byte, unpacked int) error {
	if err := d.rd.init(data); err != nil {
		return err
	}
	rd, win := &d.rd, d.win
	pbMask := uint32(1)<<d.pb - 1
	lpMask := uint32(1)<<d.lp - 1
	for left := unpacked; left > 0; {
		if rd.short {
			return invalid("LZMA chunk ends early")
		}
		posState := uint32(win.total) & pbMask
		if rd.bit(&d.isMatch[d.state<<posBitsMax+posState]) == 0 {
			d.literalByte(uint32(win.total) & lpMask)
			left--
			continue
		}
		var length uint32
		if rd.bit(&d.isRep[d.state]) != 0 {
			if win.empty() {
				return invalid("LZMA repeat in an empty dictionary")
			}
			if rd.bit(&d.isRepG0[d.state]) == 0 {
				if rd.bit(&d.isRep0Long[d.state<<posBitsMax+posState]) == 0 {
					d.state = map7(d.state, 9, 11)
					win.put(win.back(d.rep0))
					left--
					continue
				}
			} else {
				var dist uint32
				if rd.bit(&d.isRepG1[d.state]) == 0 {
					dist = d.rep1
				} else {
					if rd.bit(&d.isRepG2[d.state]) == 0 {
						dist = d.rep2
					} else {
						dist = d.rep3
						d.rep3 = d.rep2
					}
					d.rep2 = d.rep1
				}
				d.rep1 = d.rep0
				d.rep0 = dist
			}
			length = d.repLength.decode(rd, posState)
			d.state = map7(d.state, 8, 11)
		} else {
			d.rep3, d.rep2, d.rep1 = d.rep2, d.rep1, d.rep0
			length = d.length.decode(rd, posState)
			d.state = map7(d.state, 7, 10)
			d.rep0 = d.distance(length)
			if d.rep0 == 0xFFFFFFFF {
				return invalid("LZMA end marker inside LZMA2")
			}
			if !win.has(d.rep0) {
				return invalid("LZMA match reaches before the dictionary")
			}
		}
		n := int(length) + matchMinLen
		if n > left {
			return invalid("LZMA match runs past its chunk")
		}
		for range n {
			win.put(win.back(d.rep0))
		}
		left -= n
	}
	if rd.short || rd.code != 0 || rd.pos != len(rd.data) {
		return invalid("LZMA chunk does not end where its data does")
	}
	return nil
}

// literalByte decodes one literal, against the byte at rep0 after a match.
func (d *decoder) literalByte(lpBits uint32) {
	rd, win := &d.rd, d.win
	var prev uint32
	if !win.empty() {
		prev = uint32(win.back(0))
	}
	litState := lpBits<<d.lc + prev>>(8-d.lc)
	probs := d.literal[0x300*litState:]
	sym := uint32(1)
	if d.state >= 7 {
		match := uint32(win.back(d.rep0))
		for sym < 0x100 {
			matchBit := (match >> 7) & 1
			match <<= 1
			b := rd.bit(&probs[(1+matchBit)<<8+sym])
			sym = sym<<1 | b
			if matchBit != b {
				break
			}
		}
	}
	for sym < 0x100 {
		sym = sym<<1 | rd.bit(&probs[sym])
	}
	win.put(byte(sym))
	switch {
	case d.state < 4:
		d.state = 0
	case d.state < 10:
		d.state -= 3
	default:
		d.state -= 6
	}
}

// distance decodes a match distance (0-based) for a 0-based length.
func (d *decoder) distance(length uint32) uint32 {
	rd := &d.rd
	lenState := min(length, numLenToPosStates-1)
	slot := rd.tree(d.posSlot[lenState][:], 6)
	if slot < startPosModel {
		return slot
	}
	bits := int(slot>>1) - 1
	dist := (2 | slot&1) << bits
	if slot < endPosModel {
		return dist + rd.reverse(d.posDecoder[dist-slot:], bits)
	}
	dist += rd.direct(bits-numAlignBits) << numAlignBits
	return dist + rd.reverse(d.align[:], numAlignBits)
}

// map7 is the next state after a match: a when the state was a literal one (below 7), else b.
func map7(state, a, b uint32) uint32 {
	if state < 7 {
		return a
	}
	return b
}

// fill sets every probability to even.
func fill(p []uint16) {
	for i := range p {
		p[i] = probInit
	}
}
