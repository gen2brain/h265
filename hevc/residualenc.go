package hevc

func encodeResidual(w *cabacWriter, s *sps, p *pps, _ *sliceHeader, coef []int32,
	b residualBlock, _ *[4]uint8,
) error {
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

func encodeSubBlockLevels(w *cabacWriter, s *sps, p *pps, coef []int32, b residualBlock,
	sb scanPos, coeffScan []scanPos, sig *[numSbCoeff]bool, subBlock, n int,
	st *residualState,
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

	var pos [numSbCoeff]int
	npos := 0
	for k := numSbCoeff - 1; k >= 0; k-- {
		if sig[k] {
			pos[npos] = k
			npos++
		}
	}
	firstSig, lastSig := pos[npos-1], pos[0]
	signHidden := lastSig-firstSig > 3 && !b.transquantBypass

	var greater1 [numSbCoeff]bool
	numGreater1, lastGreater1, greater1Ctx := 0, -1, 1
	for _, k := range pos[:npos] {
		if numGreater1 >= 8 {
			continue
		}

		level := coefAt(coef, coeffScan[k], xS, yS, n)
		greater1[k] = absLevel(level) > 1
		ctx := ctxSet*4 + min(3, greater1Ctx)
		if b.cIdx > 0 {
			ctx += 16
		}
		w.encodeBin(ctxCoeffAbsLevelGreater1Flag+ctx, boolToBit(greater1[k]))
		numGreater1++
		st.greater1Ctx, st.lastGreater1 = greater1Ctx, greater1[k]
		if greater1[k] {
			greater1Ctx = 0
			if lastGreater1 < 0 {
				lastGreater1 = k
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
		w.encodeBin(ctxCoeffAbsLevelGreater2Flag+ctx,
			boolToBit(absLevel(coefAt(coef, coeffScan[lastGreater1], xS, yS, n)) > 2))
	}

	for _, k := range pos[:npos] {
		if p.signDataHidingEnabled && signHidden && k == firstSig {
			continue
		}
		w.encodeBypass(boolToBit(coefAt(coef, coeffScan[k], xS, yS, n) < 0))
	}

	rice, rng, numSig := 0, s.coeffRange(b.cIdx), 0
	for _, k := range pos[:npos] {
		level := absLevel(coefAt(coef, coeffScan[k], xS, yS, n))
		base := int32(1)
		if greater1[k] {
			base++
		}
		if k == lastGreater1 && level > 2 {
			base++
		}

		want := int32(1)
		if numSig < 8 {
			want = 2
			if k == lastGreater1 {
				want = 3
			}
		}
		if base == want {
			w.encodeCoeffAbsLevelRemaining(level-base, rice, rng)
			if level > 3<<rice {
				rice = min(rice+1, 4)
			}
		}
		numSig++
	}
}

func coefAt(coef []int32, pos scanPos, xS, yS, n int) int32 {
	return coef[(yS<<2+int(pos.y))*n+xS<<2+int(pos.x)]
}

func absLevel(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
