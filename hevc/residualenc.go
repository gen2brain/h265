package hevc

// encodeResidual is the residual_coding syntax of 7.3.8.11. coef holds the
// levels in raster order and must already carry the parity of 7.4.9.11.
func encodeResidual(w *cabacWriter, s *sps, p *pps, coef []int32, b residualBlock) error {
	if b.log2Size < 2 || b.log2Size > 5 || b.cIdx < 0 || b.cIdx > 2 ||
		len(coef) != 1<<(b.log2Size<<1) {
		return ErrInvalid
	}

	if p.transformSkipEnabled || s.persistentRiceAdaptation ||
		b.transquantBypass || p.transquantBypass || s.transformSkipContext || s.extendedPrecision {
		return ErrUnsupported
	}

	n := 1 << b.log2Size
	scanIdx := scanIndex(b.log2Size, b.cIdx, b.predModeIntra, b.intra, s.chromaArrayType())
	sbLog2 := b.log2Size - 2
	sbScan := scanOrder[sbLog2][scanIdx]
	coeffScan := scanOrder[2][scanIdx]

	lastSubBlock, lastScanPos := -1, -1
	var csbf [8 * 8]bool

	for i, sb := range sbScan {
		xS, yS := int(sb.x), int(sb.y)
		for k, pos := range coeffScan {
			level := coef[(yS<<2+int(pos.y))*n+xS<<2+int(pos.x)]
			if level == -1<<31 {
				return ErrInvalid
			}
			if level != 0 {
				csbf[yS*(n>>2)+xS] = true
				lastSubBlock, lastScanPos = i, k
			}
		}
	}

	if lastSubBlock < 0 {
		return ErrInvalid
	}

	lastSB := sbScan[lastSubBlock]
	lastPos := coeffScan[lastScanPos]
	lastX, lastY := int(lastSB.x)<<2+int(lastPos.x), int(lastSB.y)<<2+int(lastPos.y)
	if scanIdx == scanVer {
		lastX, lastY = lastY, lastX
	}

	xPrefix := encodeLastSigCoeffPrefix(w, ctxLastSignificantCoeffXPrefix, lastX, b.log2Size, b.cIdx)
	yPrefix := encodeLastSigCoeffPrefix(w, ctxLastSignificantCoeffYPrefix, lastY, b.log2Size, b.cIdx)
	encodeLastSigCoeffSuffix(w, lastX, xPrefix)
	encodeLastSigCoeffSuffix(w, lastY, yPrefix)

	sbWidth := n >> 2
	var st residualState

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
			w.encodeBin(base+min(ctx, 1), boolToBit(csbf[yS*sbWidth+xS]))
			inferSbDcSig = true
		}

		if !csbf[yS*sbWidth+xS] && i != 0 {
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

		var sig [numSbCoeff]bool
		if i == lastSubBlock {
			sig[lastScanPos] = true
		}

		sigSet := newSigCtxSet(xS, yS, b.log2Size, b.cIdx, scanIdx, prevCsbf, false)
		for k := start; k >= 0; k-- {
			if k == 0 && inferSbDcSig && !anyTrue(sig[1:]) {
				sig[0] = true
				continue
			}

			pos := coeffScan[k]
			level := coef[(yS<<2+int(pos.y))*n+xS<<2+int(pos.x)]
			sig[k] = level != 0
			w.encodeBin(ctxSignificantCoeffFlag+sigSet.at(int(pos.x), int(pos.y)), boolToBit(sig[k]))
		}

		encodeSubBlockLevels(w, s, p, coef, b, sb, coeffScan, &sig, i, n, &st)
	}

	return nil
}

func encodeLastSigCoeffPrefix(w *cabacWriter, base, v, log2Size, cIdx int) int {
	prefix := lastSigCoeffPrefix(v)
	cMax := log2Size<<1 - 1
	for i := 0; i < prefix; i++ {
		w.encodeBin(base+lastSigCoeffCtx(log2Size, cIdx, i), 1)
	}
	if prefix < cMax {
		w.encodeBin(base+lastSigCoeffCtx(log2Size, cIdx, prefix), 0)
	}

	return prefix
}

func encodeLastSigCoeffSuffix(w *cabacWriter, v, prefix int) {
	if prefix > 3 {
		n := prefix>>1 - 1
		w.encodeBypassBits(uint32(v-((1<<n)*(2+prefix&1))), n)
	}
}

func lastSigCoeffPrefix(v int) int {
	if v <= 3 {
		return v
	}

	n := 0
	for 1<<(n+2) <= v {
		n++
	}

	return 2*n + 2 + (v>>uint(n))&1
}

func (w *cabacWriter) encodeBypassBits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		w.encodeBypass(v >> uint(i) & 1)
	}
}

func (w *cabacWriter) encodeCoeffAbsLevelRemaining(v int32, rice, rng int) {
	if v < 3<<rice {
		for range int(v >> rice) {
			w.encodeBypass(1)
		}
		w.encodeBypass(0)
		w.encodeBypassBits(uint32(v)&((1<<rice)-1), rice)
		return
	}

	k := 0
	for v >= ((1<<(k+1))+2)<<rice {
		k++
	}

	limit := 32
	if rng > 0 {
		limit = 32 - rng
		k = min(k, limit-3)
	}

	prefix := k + 3
	for range prefix {
		w.encodeBypass(1)
	}
	if prefix < limit {
		w.encodeBypass(0)
	}

	n := k + rice
	if rng > 0 && prefix == limit {
		n = rng
	}
	w.encodeBypassBits(uint32(v-((1<<k)+2)<<rice), n)
}

// encodeSubBlockLevels writes the level passes of 7.3.8.11 for one 4x4
// sub-block.
func encodeSubBlockLevels(w *cabacWriter, s *sps, p *pps, coef []int32, b residualBlock,
	sb scanPos, coeffScan []scanPos, sig *[numSbCoeff]bool, subBlock, n int,
	st *residualState,
) {
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

	// The passes visit the significant levels in the same order, so they are
	// gathered once and then addressed by rank rather than by scan position.
	var lev [numSbCoeff]int32

	npos := 0
	origin := (int(sb.y)*n + int(sb.x)) << 2
	firstSig, lastSig := -1, -1

	for k := numSbCoeff - 1; k >= 0; k-- {
		if !sig[k] {
			continue
		}

		if lastSig < 0 {
			lastSig = k
		}

		firstSig = k

		sp := coeffScan[k]
		lev[npos] = coef[origin+int(sp.y)*n+int(sp.x)]
		npos++
	}

	if npos == 0 {
		return
	}

	signHidden := lastSig-firstSig > 3 && !b.transquantBypass

	var greater1 [numSbCoeff]bool

	lastGreater1, greater1Ctx := -1, 1

	for i := range min(npos, 8) {
		greater1[i] = absLevel(lev[i]) > 1

		ctx := ctxSet*4 + min(3, greater1Ctx)
		if b.cIdx > 0 {
			ctx += 16
		}

		w.encodeBin(ctxCoeffAbsLevelGreater1Flag+ctx, boolToBit(greater1[i]))
		st.greater1Ctx, st.lastGreater1 = greater1Ctx, greater1[i]

		if greater1[i] {
			greater1Ctx = 0

			if lastGreater1 < 0 {
				lastGreater1 = i
			}
		} else if greater1Ctx > 0 {
			greater1Ctx++
		}
	}

	if lastGreater1 >= 0 {
		ctx := ctxSet
		if b.cIdx > 0 {
			ctx += 4
		}

		w.encodeBin(ctxCoeffAbsLevelGreater2Flag+ctx, boolToBit(absLevel(lev[lastGreater1]) > 2))
	}

	for i := range npos {
		if p.signDataHidingEnabled && signHidden && i == npos-1 {
			continue
		}

		w.encodeBypass(boolToBit(lev[i] < 0))
	}

	rice, rng := 0, s.coeffRange(b.cIdx)

	for i := range npos {
		level := absLevel(lev[i])

		base := int32(1)
		if greater1[i] {
			base++
		}

		if i == lastGreater1 && level > 2 {
			base++
		}

		want := int32(1)

		if i < 8 {
			want = 2
			if i == lastGreater1 {
				want = 3
			}
		}

		if base == want {
			w.encodeCoeffAbsLevelRemaining(level-base, rice, rng)

			if level > 3<<rice {
				rice = min(rice+1, 4)
			}
		}
	}
}

func absLevel(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
