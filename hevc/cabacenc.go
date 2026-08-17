package hevc

type cabacWriter struct {
	low         uint32
	rng         uint32
	outstanding int
	firstBit    bool
	bits        *putBits
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
	lps := uint32(lpsRange[w.rng>>6&3][s])
	w.rng -= lps

	if bin != uint32(s&1) {
		w.low += w.rng
		w.rng = lps
	}

	signed := int32(s)
	if bin != uint32(s&1) {
		signed = -signed - 1
	}

	w.state[ctx] = transState[128+signed]
	w.renorm()
}

func (w *cabacWriter) encodeBypass(bin uint32) {
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

func (w *cabacWriter) encodeTerminate(bin uint32) {
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
