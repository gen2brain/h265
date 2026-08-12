package hevc

import "math"

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

	// 8.6.3 scales by levelScale[qp%6] << (qp/6) and then shifts right by
	// shift. Folding the left shift into the right one keeps the product
	// inside 32 bits: a coefficient is at most fifteen bits, the matrix entry
	// eight and levelScale seven.
	if sh := shift - qp/6; sh >= 1 && rng <= 15 {
		dequant32(coef, m, int32(levelScale[qp%6]), sh, lo, hi)

		return
	}

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

func dequant32(coef []int32, m []uint8, ls int32, sh int, lo, hi int32) {
	rnd := int32(1) << (sh - 1)

	if k := dequant32Asm; k != nil && len(coef)%8 == 0 {
		k(coef, m, ls, rnd, sh, lo, hi)

		return
	}

	if m == nil {
		for i, c := range coef {
			if c == 0 {
				continue
			}

			coef[i] = clip3((c*16*ls+rnd)>>sh, lo, hi)
		}

		return
	}

	for i, c := range coef {
		if c == 0 {
			continue
		}

		coef[i] = clip3((c*int32(m[i])*ls+rnd)>>sh, lo, hi)
	}
}

// idct is 8.6.4.2, split by size so the even half unrolls instead of
// recursing. Each level transforms the even coefficients at half the size and
// combines them with an odd half computed from the basis rows.
func idct(out, in []int32, n int, s *transformScratch) {
	switch n {
	case 4:
		idct4(out, in)
	case 8:
		idct8(out, in)
	case 16:
		idct16(out, in)
	default:
		idct32(out, in, s)
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

	oddGo(o[:], in, 4)

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
	oddGo(o[:], in, 2)

	for i, v := range e {
		out[i] = v + o[i]
		out[15-i] = v - o[i]
	}
}

func idct32(out, in []int32, s *transformScratch) {
	var ev, e [16]int32

	for i := range ev {
		ev[i] = in[2*i]
	}

	idct16(e[:], ev[:])

	o := s.odd[:]

	// Only the widest butterfly is worth an assembly call; below sixteen the
	// call costs more than the vectors save.
	if k := oddAsm; k != nil {
		k(o, in, 1)
	} else {
		oddGo(o, in, 1)
	}

	for i, v := range e {
		out[i] = v + o[i]
		out[31-i] = v - o[i]
	}
}

// oddGo accumulates the odd half of one level, a basis row at a time so the
// matrix is indexed once per coefficient and a zero one costs nothing.
func oddGo(out, in []int32, stride int) {
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
	// odd is the sixteen-wide butterfly accumulator. It lives here rather
	// than on the stack because passing a local array to a kernel through a
	// function value makes it escape.
	odd    [16]int32
	col    [32]int32
	out    [32]int32
	block  [32 * 32]int32
	block2 [32 * 32]int32
}

// idctColsGo is 8.6.4.2 over eight columns at a time, dense but for the
// even/odd split: M[j][n-1-i] is M[j][i] for even j and its negation for odd j.
func idctColsGo(dst, src []int32, n int, rnd int32, shift int, lo, hi int32) {
	half := n / 2
	step := 32 / n

	var acc [2][16 * 8]int32

	for x0 := 0; x0 < n; x0 += 8 {
		clear(acc[0][:half*8])
		clear(acc[1][:half*8])

		for j := range n {
			c := src[j*n+x0:][:8]

			var any int32
			for _, v := range c {
				any |= v
			}

			if any == 0 {
				continue
			}

			row := transMatrix[j*step][:half]
			a := acc[j&1][:half*8]

			for i, m := range row {
				w := int32(m)

				for lane, v := range c {
					a[i*8+lane] += w * v
				}
			}
		}

		for i := range half {
			e := acc[0][i*8:][:8]
			o := acc[1][i*8:][:8]

			lowRow := dst[i*n+x0:][:8]
			highRow := dst[(n-1-i)*n+x0:][:8]

			for lane := range 8 {
				lowRow[lane] = clip3((e[lane]+o[lane]+rnd)>>shift, lo, hi)
				highRow[lane] = clip3((e[lane]-o[lane]+rnd)>>shift, lo, hi)
			}
		}
	}
}

// transMatrix32 is the basis matrix at the width the kernels broadcast from.
var transMatrix32 = func() [32][32]int32 {
	var m [32][32]int32

	for j, row := range transMatrix {
		for i, v := range row {
			m[j][i] = int32(v)
		}
	}

	return m
}()

// transposeBlock writes the transpose of an n by n block.
func transposeBlock(dst, src []int32, n int) {
	if k := transposeAsm; k != nil {
		k(dst, src, n)

		return
	}

	for y := range n {
		row := src[y*n : y*n+n]

		for x, v := range row {
			dst[x*n+y] = v
		}
	}
}

// 8.6.4.1.
func inverseTransform(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch) {
	rng := transformRange(bitDepth, extended)
	lo, hi := int32(-1<<rng), int32(1<<rng-1)

	if k := idctColsAsm; k != nil && !dst && n >= 8 {
		block, block2 := s.block[:n*n], s.block2[:n*n]

		k(block, coef[:n*n], n, 64, 7, lo, hi)
		transposeBlock(block2, block, n)
		k(block, block2, n, 0, 0, math.MinInt32, math.MaxInt32)
		transposeBlock(coef[:n*n], block, n)

		return
	}

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
			idct(out, col, n, s)
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
			idct(out, row, n, s)
		}

		copy(coef[y*n:y*n+n], out)
	}
}

// coeffRange is the transform range of a component, or zero when extended
// precision is off.
func (s *sps) coeffRange(cIdx int) int {
	if !s.extendedPrecision {
		return 0
	}

	bitDepth := int(s.bitDepthLuma)
	if cIdx > 0 {
		bitDepth = int(s.bitDepthChroma)
	}

	return transformRange(bitDepth, true)
}

// wideTransform reports whether the basis row sums of 8.6.4.2 can leave
// thirty-two bits.
func wideTransform(bitDepth int, extended bool) bool {
	return transformRange(bitDepth, extended) > 15
}

func transform1DWide(out, in []int64, n int, dst bool) {
	if dst {
		for i := range 4 {
			var v int64

			for j := range 4 {
				v += int64(dstMatrix[j][i]) * in[j]
			}

			out[i] = v
		}

		return
	}

	step := 32 / n

	for i := range n {
		var v int64

		for j := range n {
			v += int64(transMatrix[j*step][i]) * in[j]
		}

		out[i] = v
	}
}

// inverseTransformWide is 8.6.4.1 in sixty-four bits, with bdShift folded into
// the row stage.
func inverseTransformWide(coef []int32, n int, dst bool, bitDepth int, shift int, s *transformScratch) {
	rng := transformRange(bitDepth, true)
	lo, hi := int64(-1<<rng), int64(1<<rng-1)

	var col, out [32]int64

	for x := range n {
		for y := range n {
			col[y] = int64(coef[y*n+x])
		}

		transform1DWide(out[:n], col[:n], n, dst)

		for y := range n {
			v := (out[y] + 64) >> 7

			s.block[y*n+x] = int32(min(max(v, lo), hi))
		}
	}

	rnd := int64(1) << (shift - 1)

	for y := range n {
		for x, v := range s.block[y*n : y*n+n] {
			col[x] = int64(v)
		}

		transform1DWide(out[:n], col[:n], n, dst)

		for x := range n {
			coef[y*n+x] = int32((out[x] + rnd) >> shift)
		}
	}
}

// transformSkipWide is 8.6.2 with bdShift folded into tsShift. Extended
// precision keeps bdShift the larger, so the net shift is to the right.
func transformSkipWide(coef []int32, n int, rotate bool, shift int) {
	sh := shift - (5 + log2(n))
	rnd := int32(1) << (sh - 1)

	if rotate {
		for i, j := 0, len(coef)-1; i < j; i, j = i+1, j-1 {
			coef[i], coef[j] = (coef[j]+rnd)>>sh, (coef[i]+rnd)>>sh
		}

		if len(coef)%2 == 1 {
			coef[len(coef)/2] = (coef[len(coef)/2] + rnd) >> sh
		}

		return
	}

	for i, v := range coef {
		coef[i] = (v + rnd) >> sh
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
