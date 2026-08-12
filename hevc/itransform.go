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

	if m == nil {
		for i, c := range coef {
			coef[i] = clip((int64(c)*16*scale + rnd) >> shift)
		}

		return
	}

	for i, c := range coef {
		coef[i] = clip((int64(c)*int64(m[i])*scale + rnd) >> shift)
	}
}

// 8.6.4.2.
func idct1D(out, in []int32, n, stride int, scratch []int32) {
	if n == 1 {
		out[0] = 64 * in[0]

		return
	}

	half := n / 2

	even := scratch[:half]
	for j := range half {
		even[j] = in[2*j]
	}

	evenOut := scratch[half : 2*half]
	idct1D(evenOut, even, half, stride*2, scratch[2*half:])

	for i := range half {
		var odd int32

		for j := range half {
			odd += int32(transMatrix[(2*j+1)*stride][i]) * in[2*j+1]
		}

		out[i] = evenOut[i] + odd
		out[n-1-i] = evenOut[i] - odd
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
	tmp   [64]int32
	block [32 * 32]int32
}

// 8.6.4.1.
func inverseTransform(coef []int32, n int, dst bool, bitDepth int, extended bool, s *transformScratch) {
	rng := transformRange(bitDepth, extended)
	lo, hi := int32(-1<<rng), int32(1<<rng-1)
	stride := 32 / n

	for x := range n {
		for y := range n {
			s.col[y] = coef[y*n+x]
		}

		if dst {
			idst1D(s.out[:4], s.col[:4])
		} else {
			idct1D(s.out[:n], s.col[:n], n, stride, s.tmp[:])
		}

		for y := range n {
			s.block[y*n+x] = clip3((s.out[y]+64)>>7, lo, hi)
		}
	}

	for y := range n {
		row := s.block[y*n : y*n+n]

		if dst {
			idst1D(s.out[:4], row)
		} else {
			idct1D(s.out[:n], row, n, stride, s.tmp[:])
		}

		copy(coef[y*n:y*n+n], s.out[:n])
	}
}

// 8.6.2, bdShift.
func residualShift(coef []int32, bitDepth int, extended bool) {
	shift := 20 - bitDepth
	if extended {
		shift = max(shift, 11)
	}

	if shift <= 0 {
		return
	}

	rnd := int32(1) << (shift - 1)
	for i, c := range coef {
		coef[i] = (c + rnd) >> shift
	}
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
