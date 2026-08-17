package hevc

type putBits struct {
	data  []byte
	nbits uint8
}

func (w *putBits) bit(v uint32) {
	if w.nbits == 0 {
		w.data = append(w.data, 0)
	}

	if v != 0 {
		w.data[len(w.data)-1] |= 1 << (7 - w.nbits)
	}

	w.nbits = (w.nbits + 1) & 7
}

func (w *putBits) bits(v uint64, n int) {
	for n > 0 {
		n--
		w.bit(uint32(v >> n & 1))
	}
}

func (w *putBits) ue(v uint32) {
	n := 0

	for x := v + 1; x > 1; x >>= 1 {
		n++
	}

	for range n {
		w.bit(0)
	}

	w.bits(uint64(v)+1, n+1)
}

func (w *putBits) se(v int32) {
	var u uint32
	if v <= 0 {
		u = uint32(-v) << 1
	} else {
		u = uint32(v)<<1 - 1
	}

	w.ue(u)
}

func (w *putBits) rbspTrailingBits() {
	w.bit(1)

	for w.nbits != 0 {
		w.bit(0)
	}
}

func (w *putBits) bytes() []byte {
	return w.data
}
