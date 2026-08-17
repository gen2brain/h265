package hevc

func forwardTransform(dst, src []int32, n, bitDepth int) {
	if bitDepth == 8 && transform8Safe(src[:n*n]) {
		forwardTransform8(dst, src, n)

		return
	}

	forwardTransformWide(dst, src, n, bitDepth)
}

func transform8Safe(src []int32) bool {
	for _, v := range src {
		if v < -255 || v > 255 {
			return false
		}
	}

	return true
}

func forwardTransform8(dst, src []int32, n int) {
	if k := forwardTransform8Asm; k != nil {
		k(dst, src, n)

		return
	}

	forwardTransform8Go(dst, src, n)
}

func forwardTransform8Go(dst, src []int32, n int) {
	shift1 := log2(n) - 1
	shift2 := log2(n) + 6
	var mid [32 * 32]int32
	stride := 32 / n

	for y := range n {
		for k := range n {
			var sum int32
			for x := range n {
				sum += int32(transMatrix[k*stride][x]) * src[y*n+x]
			}
			mid[y*n+k] = (sum + 1<<uint(shift1-1)) >> uint(shift1)
		}
	}

	for k := range n {
		for v := range n {
			var sum int32
			for y := range n {
				sum += int32(transMatrix[v*stride][y]) * mid[y*n+k]
			}
			dst[v*n+k] = (sum + 1<<uint(shift2-1)) >> uint(shift2)
		}
	}
}

func forwardTransformWide(dst, src []int32, n, bitDepth int) {
	shift1 := log2(n) + bitDepth - 9
	shift2 := log2(n) + 6
	var mid [32 * 32]int64
	stride := 32 / n

	for y := range n {
		for k := range n {
			var sum int64
			for x := range n {
				sum += int64(transMatrix[k*stride][x]) * int64(src[y*n+x])
			}
			mid[y*n+k] = (sum + 1<<uint(shift1-1)) >> uint(shift1)
		}
	}

	for k := range n {
		for v := range n {
			var sum int64
			for y := range n {
				sum += int64(transMatrix[v*stride][y]) * mid[y*n+k]
			}
			dst[v*n+k] = int32((sum + 1<<uint(shift2-1)) >> uint(shift2))
		}
	}
}

func quantize(dst, src []int32, n, qp, bitDepth int) {
	qbits := 14 + qp/6 + 15 - bitDepth - log2(n)
	scale := (int64(1)<<20 + int64(levelScale[qp%6])/2) / int64(levelScale[qp%6])
	offset := int64(1<<uint(qbits)) / 3

	for i, v := range src[:n*n] {
		abs := int64(v)
		if abs < 0 {
			abs = -abs
		}
		level := min((abs*scale+offset)>>uint(qbits), int64(0x7fff))
		if v < 0 {
			level = -level
		}
		dst[i] = int32(level)
	}
}
