package hevc

type pixel interface {
	~uint8 | ~uint16
}

const (
	intraPlanar = 0
	intraDC     = 1
	intraHor    = 10
	intraVer    = 26
)

// Table 8-4, indexed by predModeIntra - 2.
var intraPredAngle = [33]int32{
	32, 26, 21, 17, 13, 9, 5, 2, 0, -2, -5, -9, -13, -17, -21, -26, -32,
	-26, -21, -17, -13, -9, -5, -2, 0, 2, 5, 9, 13, 17, 21, 26, 32,
}

// Table 8-5, indexed by predModeIntra - 11.
var intraInvAngle = [15]int32{
	-4096, -1638, -910, -630, -482, -390, -315, -256, -315, -390, -482, -630, -910, -1638, -4096,
}

// refSamples holds p[][] flattened in the order 8.4.4.2.2 scans it: the left
// column bottom to top, the corner, then the top row left to right.
type refSamples struct {
	s [4*32 + 1]int32
	n int
}

func (r *refSamples) corner() int32          { return r.s[2*r.n] }
func (r *refSamples) top(x int) int32        { return r.s[2*r.n+1+x] }
func (r *refSamples) left(y int) int32       { return r.s[2*r.n-1-y] }
func (r *refSamples) setTop(x int, v int32)  { r.s[2*r.n+1+x] = v }
func (r *refSamples) setLeft(y int, v int32) { r.s[2*r.n-1-y] = v }

func (r *refSamples) copyFrom(src *refSamples) {
	r.n = src.n
	copy(r.s[:4*src.n+1], src.s[:4*src.n+1])
}

// substitute is 8.4.4.2.2. avail runs in the same order as s.
func (r *refSamples) substitute(avail []bool, bitDepth int) {
	n := 4*r.n + 1

	first := -1

	for i := range n {
		if avail[i] {
			first = i

			break
		}
	}

	if first < 0 {
		v := int32(1) << (bitDepth - 1)
		for i := range n {
			r.s[i] = v
		}

		return
	}

	r.s[0] = r.s[first]

	for i := 1; i < n; i++ {
		if !avail[i] {
			r.s[i] = r.s[i-1]
		}
	}
}

// filterFlag is the derivation in 8.4.4.2.3.
func filterFlag(mode, n, cIdx int, sps *sps) bool {
	if cIdx != 0 && sps.chromaArrayType() != 3 {
		return false
	}

	if mode == intraDC || n == 4 {
		return false
	}

	var thres int

	switch n {
	case 8:
		thres = 7
	case 16:
		thres = 1
	case 32:
		thres = 0
	default:
		return false
	}

	return min(abs(mode-intraVer), abs(mode-intraHor)) > thres
}

func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

// filterRef is 8.4.4.2.3.
func filterRef(r *refSamples, mode, cIdx, bitDepth int, sps *sps) {
	if sps.intraSmoothingDisabled || !filterFlag(mode, r.n, cIdx, sps) {
		return
	}

	n := r.n

	if sps.strongIntraSmoothing && cIdx == 0 && n == 32 {
		lim := int32(1) << (bitDepth - 5)

		if absI32(r.corner()+r.top(2*n-1)-2*r.top(n-1)) < lim &&
			absI32(r.corner()+r.left(2*n-1)-2*r.left(n-1)) < lim {
			c, tr, bl := r.corner(), r.top(2*n-1), r.left(2*n-1)

			for i := range 2*n - 1 {
				r.setLeft(i, ((63-int32(i))*c+int32(i+1)*bl+32)>>6)
				r.setTop(i, ((63-int32(i))*c+int32(i+1)*tr+32)>>6)
			}

			return
		}
	}

	var f [4*32 + 1]int32

	f[0] = r.s[0]
	f[4*n] = r.s[4*n]

	for i := 1; i < 4*n; i++ {
		f[i] = (r.s[i-1] + 2*r.s[i] + r.s[i+1] + 2) >> 2
	}

	copy(r.s[:4*n+1], f[:4*n+1])
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}

	return v
}

func clip1[P pixel](v int32, bitDepth int) P {
	return P(clip3(v, 0, 1<<bitDepth-1))
}

// intraPredict is 8.4.4.2.4 through 8.4.4.2.6.
func intraPredict[P pixel](dst []P, off, stride int, r *refSamples, mode, cIdx, bitDepth int) {
	switch {
	case mode == intraPlanar:
		predPlanar(dst, off, stride, r)
	case mode == intraDC:
		predDC(dst, off, stride, r, cIdx)
	default:
		predAngular(dst, off, stride, r, mode, cIdx, bitDepth)
	}
}

func predPlanar[P pixel](dst []P, off, stride int, r *refSamples) {
	n := r.n
	shift := log2(n) + 1

	if k := planarAsm; k != nil && n >= 8 {
		if p, ok := any(dst).([]uint8); ok {
			k(p[off:], stride, r, shift)

			return
		}
	}

	predPlanarGo(dst, off, stride, r, n, shift)
}

func predPlanarGo[P pixel](dst []P, off, stride int, r *refSamples, n, shift int) {
	tr, bl := r.top(n), r.left(n)

	for y := range n {
		l := r.left(y)

		for x := range n {
			v := (int32(n-1-x)*l + int32(x+1)*tr +
				int32(n-1-y)*r.top(x) + int32(y+1)*bl + int32(n)) >> shift
			dst[off+y*stride+x] = P(v)
		}
	}
}

func predDC[P pixel](dst []P, off, stride int, r *refSamples, cIdx int) {
	n := r.n

	var sum int32
	for i := range n {
		sum += r.top(i) + r.left(i)
	}

	dc := (sum + int32(n)) >> (log2(n) + 1)

	for y := range n {
		for x := range n {
			dst[off+y*stride+x] = P(dc)
		}
	}

	if cIdx != 0 || n >= 32 {
		return
	}

	dst[off] = P((r.left(0) + 2*dc + r.top(0) + 2) >> 2)

	for x := 1; x < n; x++ {
		dst[off+x] = P((r.top(x) + 3*dc + 2) >> 2)
	}

	for y := 1; y < n; y++ {
		dst[off+y*stride] = P((r.left(y) + 3*dc + 2) >> 2)
	}
}

func predAngular[P pixel](dst []P, off, stride int, r *refSamples, mode, cIdx, bitDepth int) {
	n := r.n
	angle := intraPredAngle[mode-2]

	var ref [3 * 32 * 2]int32

	base := 2 * n

	set := func(i int, v int32) { ref[base+i] = v }

	vertical := mode >= 18

	main := r.top
	side := r.left

	if !vertical {
		main, side = r.left, r.top
	}

	set(0, r.corner())
	for x := 1; x <= n; x++ {
		set(x, main(x-1))
	}

	if angle < 0 {
		if lim := int(int32(n) * angle >> 5); lim < -1 {
			inv := intraInvAngle[mode-11]

			for x := -1; x >= lim; x-- {
				i := (int32(x)*inv + 128) >> 8
				if i == 0 {
					set(x, r.corner())

					continue
				}

				set(x, side(int(i)-1))
			}
		}
	} else {
		for x := n + 1; x <= 2*n; x++ {
			set(x, main(x-1))
		}
	}

	for b := range n {
		idx := int(int32(b+1) * angle >> 5)
		fact := int32(b+1) * angle & 31
		row := ref[base+idx+1 : base+idx+2+n]

		switch {
		case fact == 0 && vertical:
			out := dst[off+b*stride : off+b*stride+n]

			for a := range out {
				out[a] = P(row[a])
			}
		case fact == 0:
			for a := range n {
				dst[off+a*stride+b] = P(row[a])
			}
		case vertical:
			out := dst[off+b*stride : off+b*stride+n]

			for a := range out {
				out[a] = P(((32-fact)*row[a] + fact*row[a+1] + 16) >> 5)
			}
		default:
			for a := range n {
				dst[off+a*stride+b] = P(((32-fact)*row[a] + fact*row[a+1] + 16) >> 5)
			}
		}
	}

	if cIdx != 0 || n >= 32 {
		return
	}

	if mode == intraVer {
		for y := range n {
			dst[off+y*stride] = clip1[P](r.top(0)+(r.left(y)-r.corner())>>1, bitDepth)
		}

		return
	}

	if mode == intraHor {
		for x := range n {
			dst[off+x] = clip1[P](r.left(0)+(r.top(x)-r.corner())>>1, bitDepth)
		}
	}
}
