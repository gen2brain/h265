package hevc

var levelScale = [6]int32{40, 45, 51, 57, 64, 72}

var dstMatrix = [4][4]int8{
	{29, 55, 74, 84},
	{74, 74, 0, -74},
	{84, -29, -74, 55},
	{55, -84, 74, -29},
}

func clip3(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}

	if v > hi {
		return hi
	}

	return v
}

func log2(n int) int {
	k := 0
	for n > 1 {
		n >>= 1
		k++
	}

	return k
}

// 8.6.3, log2TransformRange.
func transformRange(bitDepth int, extended bool) int {
	if !extended {
		return 15
	}

	return max(15, bitDepth+6)
}

// 8.6.3. m is nil when the factors are flat 16.
func dequant(coef []int32, m []uint8, n, qp, bitDepth int, extended bool) {
	rng := transformRange(bitDepth, extended)
	shift := bitDepth + log2(n) + 10 - rng
	lo, hi := int32(-1<<rng), int32(1<<rng-1)

	// The product needs more than 32 bits before 8.6.3 clips it: a level near
	// the coefficient limit times the largest scale overflows.
	scale := int64(levelScale[qp%6]) << (qp / 6)
	rnd := int64(1) << (shift - 1)

	clip := func(v int64) int32 {
		if v < int64(lo) {
			return lo
		}

		if v > int64(hi) {
			return hi
		}

		return int32(v)
	}

	// A zero coefficient scales to zero, and most of a block is zero.
	if m == nil {
		for i, c := range coef {
			if c == 0 {
				continue
			}

			coef[i] = clip((int64(c)*16*scale + rnd) >> shift)
		}

		return
	}

	for i, c := range coef {
		if c == 0 {
			continue
		}

		coef[i] = clip((int64(c)*int64(m[i])*scale + rnd) >> shift)
	}
}

// idct is 8.6.4.2, split by size so the even half unrolls instead of
// recursing. Each level transforms the even coefficients at half the size and
// combines them with an odd half computed from the basis rows.
func idct(out, in []int32, n int) {
	switch n {
	case 4:
		idct4(out, in)
	case 8:
		idct8(out, in)
	case 16:
		idct16(out, in)
	default:
		idct32(out, in)
	}
}

func idct4(out, in []int32) {
	_ = out[3]

	e0 := 64 * (in[0] + in[2])
	e1 := 64 * (in[0] - in[2])

	o0 := 83*in[1] + 36*in[3]
	o1 := 36*in[1] - 83*in[3]

	out[0] = e0 + o0
	out[1] = e1 + o1
	out[2] = e1 - o1
	out[3] = e0 - o0
}

func idct8(out, in []int32) {
	var ev, e, o [4]int32

	ev[0], ev[1], ev[2], ev[3] = in[0], in[2], in[4], in[6]
	idct4(e[:], ev[:])

	odd(o[:], in, 4)

	for i, v := range e {
		out[i] = v + o[i]
		out[7-i] = v - o[i]
	}
}

func idct16(out, in []int32) {
	var ev, e, o [8]int32

	for i := range ev {
		ev[i] = in[2*i]
	}

	idct8(e[:], ev[:])
	odd(o[:], in, 2)

	for i, v := range e {
		out[i] = v + o[i]
		out[15-i] = v - o[i]
	}
}

func idct32(out, in []int32) {
	var ev, e, o [16]int32

	for i := range ev {
		ev[i] = in[2*i]
	}

	idct16(e[:], ev[:])
	odd(o[:], in, 1)

	for i, v := range e {
		out[i] = v + o[i]
		out[31-i] = v - o[i]
	}
}

// odd accumulates the odd half of one level, a basis row at a time so the
// matrix is indexed once per coefficient and a zero one costs nothing.
func odd(out, in []int32, stride int) {
	clear(out)

	for j := range out {
		c := in[2*j+1]
		if c == 0 {
			continue
		}

		row := transMatrix[(2*j+1)*stride][:len(out)]
		acc := out[:len(row)]

		for i, v := range row {
			acc[i] += int32(v) * c
		}
	}
}

func idst1D(out, in []int32) {
	for i := range 4 {
		var v int32

		for j := range 4 {
			v += int32(dstMatrix[j][i]) * in[j]
		}

		out[i] = v
	}
}

type transformScratch struct {
	col   [32]int32
	out   [32]int32
	block [32 * 32]int32
}

// 8.6.4.1.
func inverseTransform(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch) {
	rng := transformRange(bitDepth, extended)
	lo, hi := int32(-1<<rng), int32(1<<rng-1)

	// A column of zeros transforms to zeros, and residual blocks are mostly
	// zero above the last significant coefficient.
	col, out := s.col[:n], s.out[:n]

	for x := range n {
		var acc int32

		for y := range col {
			v := coef[y*n+x]
			col[y] = v
			acc |= v
		}

		if acc == 0 {
			for y := range n {
				s.block[y*n+x] = 0
			}

			continue
		}

		if dst {
			idst1D(out, col)
		} else {
			idct(out, col, n)
		}

		for y, v := range out {
			s.block[y*n+x] = clip3((v+64)>>7, lo, hi)
		}
	}

	for y := range n {
		row := s.block[y*n : y*n+n]

		if dst {
			idst1D(out, row)
		} else {
			idct(out, row, n)
		}

		copy(coef[y*n:y*n+n], out)
	}
}

// residualShiftBits is bdShift of 8.6.2.
func residualShiftBits(bitDepth int, extended bool) int {
	shift := 20 - bitDepth
	if extended {
		return max(shift, 11)
	}

	return shift
}

// 8.6.2, transform_skip_flag. Not clipped; bdShift follows.
func transformSkip(coef []int32, n int, rotate bool) {
	shift := 5 + log2(n)

	if rotate {
		for i, j := 0, len(coef)-1; i < j; i, j = i+1, j-1 {
			coef[i], coef[j] = coef[j]<<shift, coef[i]<<shift
		}

		if len(coef)%2 == 1 {
			coef[len(coef)/2] <<= shift
		}

		return
	}

	for i := range coef {
		coef[i] <<= shift
	}
}
