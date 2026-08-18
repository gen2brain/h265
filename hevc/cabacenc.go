package hevc

// cabacWriter is the arithmetic encoder of 9.3.4. One with no bits to write to
// counts what it would have written in rate instead.
type cabacWriter struct {
	low         uint32
	rng         uint32
	outstanding int
	firstBit    bool
	bits        *putBits
	rate        int64
	state       [nContexts]uint8
}

func (w *cabacWriter) init(bits *putBits, qp int32, t sliceType, cabacInit bool) {
	w.bits = bits
	w.reinit()

	row := &initValues[initType(t, cabacInit)]
	for i := range w.state {
		w.state[i] = initState(row[i], qp)
	}
}

// counter is a copy of w that writes nothing.
func (w *cabacWriter) counter() cabacWriter {
	c := *w
	c.bits = nil
	c.rate = 0

	return c
}

func (w *cabacWriter) reinit() {
	w.low = 0
	w.rng = 510
	w.outstanding = 0
	w.firstBit = true
}

func (w *cabacWriter) putBit(v uint32) {
	if w.firstBit {
		w.firstBit = false
	} else {
		w.bits.bit(v)
	}

	for w.outstanding > 0 {
		w.bits.bit(1 - v)
		w.outstanding--
	}
}

func (w *cabacWriter) renorm() {
	for w.rng < 256 {
		switch {
		case w.low < 256:
			w.putBit(0)
		case w.low >= 512:
			w.low -= 512
			w.putBit(1)
		default:
			w.low -= 256
			w.outstanding++
		}

		w.rng <<= 1
		w.low <<= 1
	}
}

func (w *cabacWriter) encodeBin(ctx int, bin uint32) {
	s := w.state[ctx]
	mps := bin == uint32(s&1)

	signed := int32(s)
	if !mps {
		signed = -signed - 1
	}

	w.state[ctx] = transState[128+signed]

	if w.bits == nil {
		w.rate += int64(entropyBits[s^uint8(bin&1)])

		return
	}

	lps := uint32(lpsRange[w.rng>>6&3][s])
	w.rng -= lps

	if !mps {
		w.low += w.rng
		w.rng = lps
	}

	w.renorm()
}

func (w *cabacWriter) encodeBypass(bin uint32) {
	if w.bits == nil {
		w.rate += 1 << rateShift

		return
	}

	w.low <<= 1
	if bin != 0 {
		w.low += w.rng
	}

	switch {
	case w.low >= 1024:
		w.low -= 1024
		w.putBit(1)
	case w.low < 512:
		w.putBit(0)
	default:
		w.low -= 512
		w.outstanding++
	}
}

func (w *cabacWriter) encodeBypassBits(v uint32, n int) {
	if w.bits == nil {
		w.rate += int64(n) << rateShift

		return
	}

	for i := n - 1; i >= 0; i-- {
		w.encodeBypass(v >> uint(i) & 1)
	}
}

// encodeTerminate codes a bin against the fixed range of two, which is the
// probability state 63 holds.
func (w *cabacWriter) encodeTerminate(bin uint32) {
	if w.bits == nil {
		w.rate += int64(entropyBits[126+(bin&1)])

		return
	}

	w.rng -= 2
	if bin == 0 {
		w.renorm()

		return
	}

	w.low += w.rng
	w.rng = 2
	w.renorm()
	w.putBit(w.low >> 9 & 1)
	w.bits.bit(w.low >> 8 & 1)
	w.bits.bit(1)
}

func (w *cabacWriter) bytes() []byte {
	for w.bits.nbits != 0 {
		w.bits.bit(0)
	}

	return w.bits.bytes()
}
