package hevc

import (
	"math"
	"math/bits"
)

const nContexts = 179

const (
	cabacBits = 16
	cabacMask = 1<<cabacBits - 1
)

// rateShift is the fraction of a bit the rate estimates count in.
const rateShift = 15

var (
	lpsRange   [4][128]uint8
	transState [256]uint8

	// entropyBits is what one bin costs, indexed by the state with its low bit
	// set when the bin is not the most probable symbol. Derived from
	// rangeTabLPS, mid-range in each of its four buckets.
	entropyBits [128]uint32
)

func init() {
	for g := range lpsRange {
		for st := range 64 {
			lpsRange[g][2*st] = rangeTabLPS[st][g]
			lpsRange[g][2*st+1] = rangeTabLPS[st][g]
		}
	}

	for st := range 64 {
		var mps, lps float64

		for g := range 4 {
			r := float64(256+64*g) + 31.5
			l := float64(rangeTabLPS[st][g])
			mps -= math.Log2((r - l) / r)
			lps -= math.Log2(l / r)
		}

		entropyBits[2*st] = uint32(math.Round(mps / 4 * (1 << rateShift)))
		entropyBits[2*st+1] = uint32(math.Round(lps / 4 * (1 << rateShift)))
	}

	for st := range 64 {
		mps := st + 1
		if st >= 62 {
			mps = st
		}

		lps := int(transIdxLPS[st])

		for v := range 2 {
			s := 2*st + v

			transState[128+s] = uint8(2*mps + v)

			flipped := v
			if st == 0 {
				flipped = 1 - v
			}

			transState[127-s] = uint8(2*lps + flipped)
		}
	}
}

func normShift(r uint32) uint {
	return uint(9 - bits.Len32(r))
}

type cabac struct {
	low   uint32
	rng   uint32
	data  []byte
	pos   int
	state [nContexts]uint8
}

func (c *cabac) init(data []byte, off int) error {
	if off < 0 || off+2 > len(data) {
		return ErrInvalid
	}

	c.data = data
	c.low = uint32(data[off])<<18 + uint32(data[off+1])<<10 + 1<<9
	c.rng = 0x1fe
	c.pos = off + 2

	return nil
}

func (c *cabac) initContexts(qp int32, t sliceType, cabacInit bool) {
	row := &initValues[initType(t, cabacInit)]

	for i := range c.state {
		c.state[i] = initState(row[i], qp)
	}
}

func initType(t sliceType, cabacInit bool) int {
	n := 2 - int(t)
	if cabacInit && t != sliceI {
		n ^= 3
	}

	return n
}

func initState(v uint8, qp int32) uint8 {
	m := int32(v>>4)*5 - 45
	n := int32(v&0x0f)<<3 - 16

	if qp < 0 {
		qp = 0
	} else if qp > 51 {
		qp = 51
	}

	s := 2*((m*qp>>4)+n) - 127
	s ^= s >> 31

	if s > 124 {
		s = 124 + s&1
	}

	return uint8(s)
}

func (c *cabac) byteAt(i int) uint32 {
	if i < len(c.data) {
		return uint32(c.data[i])
	}

	return 0
}

func (c *cabac) refill() {
	c.low += c.byteAt(c.pos)<<9 + c.byteAt(c.pos+1)<<1 - cabacMask
	c.pos += 2
}

func (c *cabac) refillShifted() {
	i := uint(bits.TrailingZeros32(c.low) - cabacBits)
	x := c.byteAt(c.pos)<<9 + c.byteAt(c.pos+1)<<1 - cabacMask
	c.low += x << i
	c.pos += 2
}

func (c *cabac) decodeBin(ctx int) uint32 {
	s := c.state[ctx]
	lps := uint32(lpsRange[c.rng>>6&3][s])

	c.rng -= lps

	mask := uint32(int32(c.rng<<(cabacBits+1)-c.low) >> 31)

	c.low -= c.rng << (cabacBits + 1) & mask
	c.rng += (lps - c.rng) & mask

	signed := int32(s) ^ int32(mask)
	c.state[ctx] = transState[128+signed]

	shift := normShift(c.rng)
	c.rng <<= shift
	c.low <<= shift

	if c.low&cabacMask == 0 {
		c.refillShifted()
	}

	return uint32(signed & 1)
}

func (c *cabac) decodeBypass() uint32 {
	c.low += c.low
	if c.low&cabacMask == 0 {
		c.refill()
	}

	r := c.rng << (cabacBits + 1)
	if c.low < r {
		return 0
	}

	c.low -= r

	return 1
}

func (c *cabac) decodeBypassBits(n int) uint32 {
	var v uint32

	for range n {
		v = v<<1 | c.decodeBypass()
	}

	return v
}

func (c *cabac) decodeTerminate() uint32 {
	c.rng -= 2

	if c.low >= c.rng<<(cabacBits+1) {
		return 1
	}

	if c.rng < 256 {
		c.rng <<= 1
		c.low <<= 1

		if c.low&cabacMask == 0 {
			c.refill()
		}
	}

	return 0
}

func (c *cabac) pcmOffset() int {
	n := c.pos

	if c.low&0x1 != 0 {
		n--
	}

	if c.low&0x1ff != 0 {
		n--
	}

	return n
}
