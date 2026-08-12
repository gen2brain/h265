package hevc

const numSbCoeff = 16

// Table 9-43, the sig_coeff_flag context map for 4x4 blocks.
var sigCtxMap4x4 = [16]uint8{0, 1, 4, 5, 2, 3, 4, 5, 6, 6, 8, 8, 7, 7, 8, 8}

// scanIndex is the derivation in 7.4.9.11: only small intra blocks depart from
// the diagonal scan, and then only for near-horizontal or near-vertical modes.
func scanIndex(log2Size, cIdx, predModeIntra int, intra bool, chromaArrayType uint32) int {
	if !intra {
		return scanDiag
	}

	if log2Size != 2 && !(log2Size == 3 && (cIdx == 0 || chromaArrayType == 3)) {
		return scanDiag
	}

	switch {
	case predModeIntra >= 6 && predModeIntra <= 14:
		return scanVer
	case predModeIntra >= 22 && predModeIntra <= 30:
		return scanHor
	default:
		return scanDiag
	}
}

// lastSigCoeffPrefix is the truncated Rice prefix of 9.3.3.2 with the context
// derivation of 9.3.4.2.3.
func lastSigCoeffCtx(log2Size, cIdx, binIdx int) int {
	if cIdx == 0 {
		return binIdx>>((log2Size+1)>>2) + 3*(log2Size-2) + (log2Size-1)>>2
	}

	return binIdx>>(log2Size-2) + 15
}

func (c *cabac) lastSigCoeffPrefix(base, log2Size, cIdx int) int {
	cMax := log2Size<<1 - 1

	v := 0
	for v < cMax && c.decodeBin(base+lastSigCoeffCtx(log2Size, cIdx, v)) != 0 {
		v++
	}

	return v
}

func (c *cabac) lastSigCoeffSuffix(prefix int) int {
	if prefix <= 3 {
		return prefix
	}

	n := prefix>>1 - 1
	suffix := int(c.decodeBypassBits(n))

	return (1<<n)*(2+prefix&1) + suffix
}

// coeffAbsLevelRemaining is the binarization of 9.3.3.11. rng selects the
// limited form of 9.3.3.4 and is zero otherwise.
func (c *cabac) coeffAbsLevelRemaining(rice, rng int) int32 {
	limit := 32
	if rng > 0 {
		limit = 32 - rng
	}

	prefix := 0
	for prefix < limit && c.decodeBypass() != 0 {
		prefix++
	}

	if prefix < 3 {
		return int32(prefix<<rice) + int32(c.decodeBypassBits(rice))
	}

	k := prefix - 3

	if rng > 0 && prefix == limit {
		return int32((1<<k+2)<<rice) + int32(c.decodeBypassBits(rng))
	}

	return int32((1<<k+2)<<rice) + int32(c.decodeBypassBits(k+rice))
}

// sigCtxSet is 9.3.4.2.5 with everything that is fixed for a sub-block already
// resolved, so a coefficient costs one table lookup and an add.
type sigCtxSet struct {
	base     int
	dc       int
	small    int
	prevCsbf int
	is4x4    bool
	sbDC     bool

	// 9.3.4.2.5 gives a skipped block one context for every position.
	flat bool
}

func newSigCtxSet(xS, yS, log2Size, cIdx, scanIdx, prevCsbf int, flat bool) sigCtxSet {
	c := sigCtxSet{
		prevCsbf: prevCsbf,
		is4x4:    log2Size == 2,
		sbDC:     xS|yS == 0,
		flat:     flat,
	}

	if flat {
		c.base = 42
		if cIdx != 0 {
			c.base = 27 + 16
		}

		return c
	}

	if cIdx != 0 {
		c.dc, c.small = 27, 27
		c.base = 27 + 12

		if log2Size == 3 {
			c.base = 27 + 9
		}

		return c
	}

	if xS+yS > 0 {
		c.base = 3
	}

	switch {
	case log2Size == 3 && scanIdx == scanDiag:
		c.base += 9
	case log2Size == 3:
		c.base += 15
	default:
		c.base += 21
	}

	return c
}

func (c sigCtxSet) at(x, y int) int {
	switch {
	case c.flat:
		return c.base
	case c.is4x4:
		return c.small + int(sigCtxMap4x4[y<<2+x])
	case c.sbDC && x|y == 0:
		return c.dc
	}

	return c.base + int(sigCtxByCsbf[c.prevCsbf][y&3<<2|x&3])
}

// sigCoeffCtx is 9.3.4.2.5 transcribed, which sigCtxSet is held to by
// TestSigCtxSetMatchesDerivation.
func sigCoeffCtx(xC, yC, log2Size, cIdx, scanIdx int, prevCsbf int) int {
	var sig int

	switch {
	case log2Size == 2:
		sig = int(sigCtxMap4x4[yC<<2+xC])
	case xC+yC == 0:
		sig = 0
	default:
		sig = int(sigCtxByCsbf[prevCsbf][yC&3<<2|xC&3])

		if cIdx == 0 {
			if xC>>2+yC>>2 > 0 {
				sig += 3
			}

			if log2Size == 3 {
				if scanIdx == scanDiag {
					sig += 9
				} else {
					sig += 15
				}
			} else {
				sig += 21
			}
		} else {
			if log2Size == 3 {
				sig += 9
			} else {
				sig += 12
			}
		}
	}

	if cIdx == 0 {
		return sig
	}

	return 27 + sig
}

type residualBlock struct {
	log2Size         int
	cIdx             int
	predModeIntra    int
	intra            bool
	transquantBypass bool
}

// decodeResidual is the residual_coding syntax of 7.3.8.11. coef receives the
// transform coefficient levels in raster order and must be zeroed by the
// caller.
func decodeResidual(c *cabac, s *sps, p *pps, sh *sliceHeader, coef []int32,
	b residualBlock, statCoeff *[4]uint8,
) (transformSkip bool, err error) {
	n := 1 << b.log2Size

	if p.transformSkipEnabled && !b.transquantBypass &&
		b.log2Size <= int(p.log2MaxTransformSkipSize) {
		transformSkip = c.decodeBin(ctxTransformSkipFlag+min(b.cIdx, 1)) != 0
	}

	// 9.3.4.2.5 gives a block that skips the transform one significance
	// context throughout, rather than one derived from the position.
	flatCtx := s.transformSkipContext && (transformSkip || b.transquantBypass)

	scanIdx := scanIndex(b.log2Size, b.cIdx, b.predModeIntra, b.intra, s.chromaArrayType())

	xPrefix := c.lastSigCoeffPrefix(ctxLastSignificantCoeffXPrefix, b.log2Size, b.cIdx)
	yPrefix := c.lastSigCoeffPrefix(ctxLastSignificantCoeffYPrefix, b.log2Size, b.cIdx)

	lastX := c.lastSigCoeffSuffix(xPrefix)
	lastY := c.lastSigCoeffSuffix(yPrefix)

	if scanIdx == scanVer {
		lastX, lastY = lastY, lastX
	}

	if lastX >= n || lastY >= n {
		return transformSkip, ErrInvalid
	}

	sbLog2 := b.log2Size - 2
	sbScan := scanOrder[sbLog2][scanIdx]
	coeffScan := scanOrder[2][scanIdx]

	lastSubBlock := len(sbScan) - 1
	lastScanPos := numSbCoeff

	for {
		if lastScanPos == 0 {
			lastScanPos = numSbCoeff
			lastSubBlock--

			if lastSubBlock < 0 {
				return transformSkip, ErrInvalid
			}
		}

		lastScanPos--

		sb := sbScan[lastSubBlock]
		pos := coeffScan[lastScanPos]

		if int(sb.x)<<2+int(pos.x) == lastX && int(sb.y)<<2+int(pos.y) == lastY {
			break
		}
	}

	var (
		csbf [8 * 8]bool
		st   residualState
	)

	sbWidth := 1 << sbLog2

	var sig [numSbCoeff]bool

	for i := lastSubBlock; i >= 0; i-- {
		sb := sbScan[i]
		xS, yS := int(sb.x), int(sb.y)

		inferSbDcSig := false

		if i < lastSubBlock && i > 0 {
			ctx := 0
			if xS+1 < sbWidth && csbf[yS*sbWidth+xS+1] {
				ctx++
			}

			if yS+1 < sbWidth && csbf[(yS+1)*sbWidth+xS] {
				ctx++
			}

			base := ctxSignificantCoeffGroupFlag
			if b.cIdx > 0 {
				base += 2
			}

			csbf[yS*sbWidth+xS] = c.decodeBin(base+min(ctx, 1)) != 0
			inferSbDcSig = true
		} else {
			csbf[yS*sbWidth+xS] = true
		}

		if !csbf[yS*sbWidth+xS] {
			continue
		}

		prevCsbf := 0
		if xS+1 < sbWidth && csbf[yS*sbWidth+xS+1] {
			prevCsbf++
		}

		if yS+1 < sbWidth && csbf[(yS+1)*sbWidth+xS] {
			prevCsbf += 2
		}

		start := numSbCoeff - 1
		if i == lastSubBlock {
			start = lastScanPos - 1
		}

		for k := range sig {
			sig[k] = false
		}

		if i == lastSubBlock {
			sig[lastScanPos] = true
		}

		// 9.3.4.2.5 varies with the coefficient only through its place inside
		// the sub-block; everything else is fixed for the whole of it.
		sigSet := newSigCtxSet(xS, yS, b.log2Size, b.cIdx, scanIdx, prevCsbf, flatCtx)

		for k := start; k >= 0; k-- {
			if k == 0 && inferSbDcSig && !anyTrue(sig[1:]) {
				sig[0] = true

				continue
			}

			pos := coeffScan[k]

			ctx := sigSet.at(int(pos.x), int(pos.y))
			sig[k] = c.decodeBin(ctxSignificantCoeffFlag+ctx) != 0
		}

		decodeSubBlockLevels(c, s, p, coef, b, sbScan[i], coeffScan,
			&sig, i, n, statCoeff, &st)
	}

	return transformSkip, nil
}

func anyTrue(v []bool) bool {
	for _, b := range v {
		if b {
			return true
		}
	}

	return false
}

// residualState carries the greater1 context across the sub-blocks of one
// transform block, as 9.3.4.2.6 requires.
type residualState struct {
	started      bool
	greater1Ctx  int
	lastGreater1 bool
}

func decodeSubBlockLevels(c *cabac, s *sps, p *pps, coef []int32,
	b residualBlock, sb scanPos, coeffScan []scanPos, sig *[numSbCoeff]bool,
	subBlock, n int, statCoeff *[4]uint8, st *residualState,
) {
	xS, yS := int(sb.x), int(sb.y)

	ctxSet := 0
	if subBlock > 0 && b.cIdx == 0 {
		ctxSet = 2
	}

	lastCtx := 1

	if st.started {
		lastCtx = st.greater1Ctx
		if lastCtx > 0 {
			if st.lastGreater1 {
				lastCtx = 0
			} else {
				lastCtx++
			}
		}
	}

	if lastCtx == 0 {
		ctxSet++
	}

	st.started = true

	// The three passes below visit the significant coefficients only, so the
	// positions are gathered once instead of rescanning all sixteen.
	var posBuf [numSbCoeff]int8

	npos := 0

	for k := numSbCoeff - 1; k >= 0; k-- {
		if sig[k] {
			posBuf[npos] = int8(k)
			npos++
		}
	}

	if npos == 0 {
		return
	}

	pos := posBuf[:npos]

	firstSig, lastSig := int(pos[len(pos)-1]), int(pos[0])
	lastGreater1 := -1

	var greater1 [numSbCoeff]bool

	numGreater1 := 0
	greater1Ctx := 1

	for _, p8 := range pos {
		k := int(p8)

		if numGreater1 < 8 {
			ctx := ctxSet*4 + min(3, greater1Ctx)
			if b.cIdx > 0 {
				ctx += 16
			}

			greater1[k] = c.decodeBin(ctxCoeffAbsLevelGreater1Flag+ctx) != 0
			numGreater1++

			st.greater1Ctx = greater1Ctx
			st.lastGreater1 = greater1[k]

			if greater1[k] {
				greater1Ctx = 0

				if lastGreater1 < 0 {
					lastGreater1 = k
				}
			} else if greater1Ctx > 0 {
				greater1Ctx++
			}
		}
	}

	var greater2 bool

	if lastGreater1 >= 0 {
		ctx := ctxSet
		if b.cIdx > 0 {
			ctx += 4
		}

		greater2 = c.decodeBin(ctxCoeffAbsLevelGreater2Flag+ctx) != 0
	}

	signHidden := lastSig-firstSig > 3 && !b.transquantBypass

	var signs [numSbCoeff]bool

	for _, p8 := range pos {
		k := int(p8)

		if p.signDataHidingEnabled && signHidden && k == firstSig {
			continue
		}

		signs[k] = c.decodeBypass() != 0
	}

	rice := 0
	if s.persistentRiceAdaptation {
		rice = int(statCoeff[riceStatIndex(b)] / 4)
	}

	rng := s.coeffRange(b.cIdx)

	numSig := 0

	var sumAbs int32

	firstRemaining := true

	for _, p8 := range pos {
		k := int(p8)

		base := int32(1)
		if greater1[k] {
			base++
		}

		if k == lastGreater1 && greater2 {
			base++
		}

		want := int32(1)

		if numSig < 8 {
			want = 2
			if k == lastGreater1 {
				want = 3
			}
		}

		level := base

		if base == want {
			rem := c.coeffAbsLevelRemaining(rice, rng)
			level = base + rem

			if s.persistentRiceAdaptation && firstRemaining {
				updateStatCoeff(statCoeff, riceStatIndex(b), rem)
				firstRemaining = false
			}

			if level > 3<<rice {
				rice = min(rice+1, 4)
			}
		}

		sumAbs += level

		if signs[k] {
			level = -level
		}

		if p.signDataHidingEnabled && signHidden && k == firstSig && sumAbs%2 == 1 {
			level = -level
		}

		pos := coeffScan[k]
		xC, yC := xS<<2+int(pos.x), yS<<2+int(pos.y)
		coef[yC*n+xC] = level

		numSig++
	}
}

func riceStatIndex(b residualBlock) int {
	if b.cIdx == 0 {
		return 0
	}

	return 2
}

func updateStatCoeff(stat *[4]uint8, i int, rem int32) {
	switch {
	case rem >= 3<<(stat[i]/4):
		stat[i]++
	case 2*rem < 1<<(stat[i]/4) && stat[i] > 0:
		stat[i]--
	}
}

// sigCtxByCsbf is the position part of 9.3.4.2.5, which depends only on the
// coefficient's place within its sub-block and on the two neighbouring
// sub-block flags. Built from the derivation rather than transcribed.
var sigCtxByCsbf = func() [4][numSbCoeff]uint8 {
	var t [4][numSbCoeff]uint8

	for csbf := range 4 {
		for yP := range 4 {
			for xP := range 4 {
				var sig uint8

				switch csbf {
				case 0:
					switch {
					case xP+yP == 0:
						sig = 2
					case xP+yP < 3:
						sig = 1
					}
				case 1:
					switch {
					case yP == 0:
						sig = 2
					case yP == 1:
						sig = 1
					}
				case 2:
					switch {
					case xP == 0:
						sig = 2
					case xP == 1:
						sig = 1
					}
				default:
					sig = 2
				}

				t[csbf][yP<<2|xP] = sig
			}
		}
	}

	return t
}()
