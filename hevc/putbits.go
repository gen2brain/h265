package hevc

type putBits struct {
	data  []byte
	cur   uint8
	nbits uint8
}

func (w *putBits) bit(v uint32) {
	w.cur = w.cur<<1 | uint8(v&1)
	w.nbits++

	if w.nbits == 8 {
		w.data = append(w.data, w.cur)
		w.cur, w.nbits = 0, 0
	}
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

// count is the number of bits written, which the rate estimates measure.
func (w *putBits) count() int {
	return len(w.data)*8 + int(w.nbits)
}

func (w *putBits) bytes() []byte {
	return w.data
}
