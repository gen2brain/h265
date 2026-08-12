package hevc

import "math/bits"

type getBits struct {
	state    uint64
	bitsLeft int
	err      bool
	index    int
	data     []byte
}

func (c *getBits) init(data []byte) {
	*c = getBits{data: data}
}

func (c *getBits) refill(n int) {
	var state uint32

	for {
		if c.index >= len(c.data) {
			c.err = true
			if state != 0 {
				break
			}

			return
		}

		state = state<<8 | uint32(c.data[c.index])
		c.index++
		c.bitsLeft += 8

		if n <= c.bitsLeft {
			break
		}
	}

	c.state |= uint64(state) << (64 - c.bitsLeft)
}

func (c *getBits) bit() uint32 {
	if c.bitsLeft == 0 {
		c.refill(1)
	}

	state := c.state
	c.bitsLeft--
	c.state = state << 1

	return uint32(state >> 63)
}

func (c *getBits) bits(n int) uint32 {
	if n == 0 {
		return 0
	}

	if uint32(n) > uint32(c.bitsLeft) {
		c.refill(n)
	}

	state := c.state
	c.bitsLeft -= n
	c.state = state << n

	return uint32(state >> (64 - n))
}

func (c *getBits) skip(n int) {
	for n > 32 {
		c.bits(32)
		n -= 32
	}

	c.bits(n)
}

func (c *getBits) ue() uint32 {
	n := 0

	for c.bit() == 0 {
		n++

		if n > 31 {
			c.err = true

			return 0
		}
	}

	if n == 0 {
		return 0
	}

	return 1<<n - 1 + c.bits(n)
}

func (c *getBits) se() int32 {
	v := c.ue()
	k := int32((v + 1) / 2)

	if v&1 == 0 {
		return -k
	}

	return k
}

func (c *getBits) pos() int {
	return c.index*8 - c.bitsLeft
}

func (c *getBits) byteAlign() {
	if n := c.pos() & 7; n != 0 {
		c.bits(8 - n)
	}
}

func (c *getBits) moreRBSPData() bool {
	p := c.pos()
	byteOff, bitOff := p>>3, p&7

	if byteOff >= len(c.data) {
		return false
	}

	last := len(c.data)
	for last > byteOff && c.data[last-1] == 0 {
		last--
	}

	if last <= byteOff {
		return false
	}

	if byteOff < last-1 {
		return true
	}

	return bitOff < 7-bits.TrailingZeros8(c.data[byteOff])
}
