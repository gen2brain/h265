package hevc

// Rate distortion optimised quantisation. Rounding a coefficient to the nearest
// level is only right when bits are free; 8.6.3 leaves the encoder to weigh the
// error a coarser level makes against the bits it saves, which is what this
// does, one coefficient at a time and in the order 7.3.8.11 codes them.

// quantScale is the multiplier of 8.6.3 and the shift that follows it.
func quantScale(n, qp, bitDepth int) (scale int64, qbits uint) {
	qbits = uint(14 + qp/6 + 15 - bitDepth - log2(n))
	scale = (int64(1)<<20 + int64(levelScale[qp%6])/2) / int64(levelScale[qp%6])

	return scale, qbits
}

// levelDist is the squared picture error one level of the quantiser makes,
// scaled so that levelError's shifted square lands in rdCost's units. A level
// is levelScale[qp%6] << (qp/6) over 64 in the picture, which is the square
// root of this.
func levelDist(qp int) int64 {
	return int64(levelScale[qp%6]) * int64(levelScale[qp%6]) << (2 * (qp / 6))
}

// levelError is the squared error a level leaves, in the units rdCost weighs
// against bits. A level of the quantiser is levelScale[qp%6] << (qp/6) over 64
// in the picture, which is where the constant comes from; the shift by 15 keeps
// the square inside 64 bits, and the 19 it is short is carried by the rate.
func levelError(err int64, shift uint, dist int64) int64 {
	e := err >> shift

	return e * e * dist
}

// rdoqState carries what the level passes of 7.3.8.11 make the next coefficient
// cost: which context set the greater-than-one flags are in, how far the
// greater-than-one run has got, and the Rice parameter.
type rdoqState struct {
	ctxSet       int
	greater1Ctx  int
	lastGreater1 int
	numSig       int
	rice         int
	started      bool
	last         bool
}

// binBits is what one context coded bin costs against the contexts as they
// stand, which is the estimate 9.3.4.3 would give it.
func (e *intraEncoder[P]) binBits(ctx int, bin uint32) int64 {
	return int64(entropyBits[e.cabac.state[ctx]^uint8(bin&1)])
}

// remainingBits is the length of the 9.3.3.11 binarisation, all of it bypass.
func remainingBits(v int32, rice int) int64 {
	if v < 3<<rice {
		return int64(v>>rice+1+int32(rice)) << rateShift
	}

	k := 0
	for v >= ((1<<(k+1))+2)<<rice {
		k++
	}

	return int64(2*k+4+rice) << rateShift
}

// levelBits is what coding one significant coefficient of the given magnitude
// costs, sign included, and how the passes move on.
func (e *intraEncoder[P]) levelBits(st *rdoqState, level int32, cIdx int) int64 {
	bits := int64(1) << rateShift

	base := int32(1)
	want := int32(1)

	if st.numSig < 8 {
		ctx := st.ctxSet*4 + min(3, st.greater1Ctx)
		if cIdx > 0 {
			ctx += 16
		}

		bits += e.binBits(ctxCoeffAbsLevelGreater1Flag+ctx, boolToBit(level > 1))
		want = 2

		if level > 1 {
			base = 2

			if st.lastGreater1 < 0 {
				ctx := st.ctxSet
				if cIdx > 0 {
					ctx += 4
				}

				bits += e.binBits(ctxCoeffAbsLevelGreater2Flag+ctx, boolToBit(level > 2))
				want = 3

				if level > 2 {
					base = 3
				}
			}
		}
	}

	if base == want {
		bits += remainingBits(level-base, st.rice)
	}

	return bits
}

// advance moves the level passes on by one coded coefficient.
func (st *rdoqState) advance(level int32) {
	if st.numSig < 8 {
		if level > 1 {
			if st.lastGreater1 < 0 {
				st.lastGreater1 = st.numSig
			}

			st.greater1Ctx = 0
		} else if st.greater1Ctx > 0 {
			st.greater1Ctx++
		}
	}

	if level > 3<<st.rice {
		st.rice = min(st.rice+1, 4)
	}

	st.numSig++
}

// rdoq quantises raw into coef, choosing each level for what it costs rather
// than for how near it is.
func (e *intraEncoder[P]) rdoq(coef, raw []int32, n, qp, cIdx, mode int) {
	scale, qbits := quantScale(n, qp, e.bitDepth)

	// Only the ratio of error to bits is weighed here, so both halves run at
	// the eight bit scale, which is the one the squares fit inside.
	dist := levelDist(qp - 6*(e.bitDepth-8))
	shift := qbits - 15
	lambda := e.lambdaBase << 19

	scanIdx := scanIndex(log2(n), cIdx, mode, true, e.s.chromaArrayType())
	sbScan := scanOrder[log2(n)-2][scanIdx]
	coeffScan := scanOrder[2][scanIdx]
	sbWidth := n >> 2

	num := e.scratch.num[:n*n]
	for i, v := range raw[:n*n] {
		t := int64(v)
		if t < 0 {
			t = -t
		}

		num[i] = t * scale
	}

	// The significance of a sub-block feeds the contexts of the ones before it,
	// so it is taken from where the levels round to rather than from decisions
	// not yet made.
	var csbf [8 * 8]bool

	lastSB, lastPos := -1, -1

	for i, sb := range sbScan {
		for k, pos := range coeffScan {
			idx := (int(sb.y)<<2+int(pos.y))*n + int(sb.x)<<2 + int(pos.x)
			if (num[idx]+int64(1)<<(qbits-1))>>qbits == 0 {
				continue
			}

			csbf[int(sb.y)*sbWidth+int(sb.x)] = true
			lastSB, lastPos = i, k
		}
	}

	clear(coef[:n*n])

	if lastSB < 0 {
		return
	}

	// Each coefficient's cost is kept so that the last significant position can
	// be reconsidered once every level is known.
	cost := e.scratch.cost[:n*n]
	costZero := e.scratch.costZero[:n*n]
	costSig := e.scratch.costSig[:n*n]

	clear(cost)
	clear(costZero)
	clear(costSig)

	var st rdoqState

	for i := lastSB; i >= 0; i-- {
		sb := sbScan[i]
		xS, yS := int(sb.x), int(sb.y)

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

		sigSet := newSigCtxSet(xS, yS, log2(n), cIdx, scanIdx, prevCsbf, false)

		st.ctxSet = 0
		if i > 0 && cIdx == 0 {
			st.ctxSet = 2
		}

		if st.started && st.greater1Ctx == 0 {
			st.ctxSet++
		}

		st.started = true
		st.greater1Ctx = 1
		st.lastGreater1 = -1
		st.numSig = 0

		// A sub-block whose levels cost more than the error of dropping them
		// is dropped, which is a saving the flag itself pays for.
		coded, dropped := int64(0), int64(0)
		optional := i > 0 && i < lastSB

		start := numSbCoeff - 1
		if i == lastSB {
			start = lastPos
		}

		for k := start; k >= 0; k-- {
			pos := coeffScan[k]
			idx := (yS<<2+int(pos.y))*n + xS<<2 + int(pos.x)
			scan := i*numSbCoeff + k
			t := num[idx]

			sig := ctxSignificantCoeffFlag + sigSet.at(int(pos.x), int(pos.y))
			nearest := int32((t + int64(1)<<(qbits-1)) >> qbits)
			zero := levelError(t, shift, dist)

			best := int32(0)
			chosen := zero + lambda*e.binBits(sig, 0)
			sigBits := lambda * e.binBits(sig, 0)

			for level := nearest; level >= max(1, nearest-1); level-- {
				c := levelError(t-int64(level)<<qbits, shift, dist) +
					lambda*(e.binBits(sig, 1)+e.levelBits(&st, level, cIdx))
				if c < chosen {
					best, chosen = level, c
					sigBits = lambda * e.binBits(sig, 1)
				}
			}

			cost[scan] = chosen
			costZero[scan] = zero
			costSig[scan] = sigBits

			dropped += zero
			coded += chosen

			if best == 0 {
				continue
			}

			if raw[idx] < 0 {
				coef[idx] = -min(best, 0x7fff)
			} else {
				coef[idx] = min(best, 0x7fff)
			}

			st.advance(best)
		}

		if !optional {
			continue
		}

		ctx := ctxSignificantCoeffGroupFlag + min(prevCsbf, 1)
		if cIdx > 0 {
			ctx += 2
		}

		if dropped+lambda*e.binBits(ctx, 0) >= coded+lambda*e.binBits(ctx, 1) {
			continue
		}

		csbf[yS*sbWidth+xS] = false

		for k, pos := range coeffScan {
			coef[(yS<<2+int(pos.y))*n+xS<<2+int(pos.x)] = 0

			scan := i*numSbCoeff + k
			cost[scan], costSig[scan] = costZero[scan], 0
		}
	}

	e.truncate(coef, cost, costZero, costSig, sbScan, coeffScan,
		lastSB*numSbCoeff+lastPos, n, cIdx, scanIdx, lambda)
}

// truncate reconsiders where the block ends. Coding a nearer coefficient as the
// last one drops everything past it, which pays for itself when what is dropped
// costs more in bits than it is worth in error.
func (e *intraEncoder[P]) truncate(coef []int32, cost, costZero, costSig []int64,
	sbScan, coeffScan []scanPos, last, n, cIdx, scanIdx int, lambda int64,
) {
	at := func(scan int) int {
		sb, pos := sbScan[scan/numSbCoeff], coeffScan[scan%numSbCoeff]

		return (int(sb.y)<<2+int(pos.y))*n + int(sb.x)<<2 + int(pos.x)
	}

	base := int64(0)
	for scan := 0; scan <= last; scan++ {
		base += cost[scan]
	}

	best, bestLast := int64(1)<<62, last

	for scan := last; scan >= 0; scan-- {
		idx := at(scan)

		level := absLevel(coef[idx])
		if level == 0 {
			base -= costSig[scan]

			continue
		}

		if total := base - costSig[scan] + lambda*e.lastBits(idx, n, cIdx, scanIdx); total < best {
			best, bestLast = total, scan
		}

		// Past a level above one there is nothing left worth dropping.
		if level > 1 {
			break
		}

		base += costZero[scan] - cost[scan]
	}

	for scan := bestLast + 1; scan <= last; scan++ {
		coef[at(scan)] = 0
	}
}

// lastBits is what last_sig_coeff_x and last_sig_coeff_y cost for a position,
// prefix contexts and bypass suffix together.
func (e *intraEncoder[P]) lastBits(idx, n, cIdx, scanIdx int) int64 {
	x, y := idx%n, idx/n
	if scanIdx == scanVer {
		x, y = y, x
	}

	return e.lastCoordBits(ctxLastSignificantCoeffXPrefix, x, log2(n), cIdx) +
		e.lastCoordBits(ctxLastSignificantCoeffYPrefix, y, log2(n), cIdx)
}

func (e *intraEncoder[P]) lastCoordBits(base, v, log2Size, cIdx int) int64 {
	prefix := lastSigCoeffPrefix(v)
	cMax := log2Size<<1 - 1

	var bits int64

	for i := range prefix {
		bits += e.binBits(base+lastSigCoeffCtx(log2Size, cIdx, i), 1)
	}

	if prefix < cMax {
		bits += e.binBits(base+lastSigCoeffCtx(log2Size, cIdx, prefix), 0)
	}

	if prefix > 3 {
		bits += int64(prefix>>1-1) << rateShift
	}

	return bits
}
