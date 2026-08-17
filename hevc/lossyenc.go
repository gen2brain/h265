package hevc

func encodeIntraLossy(y, cb, cr []uint8, width, height int) ([]NALUnit, error) {
	return encodeIntraLossyQP(y, cb, cr, width, height, 26)
}

func encodeIntraLossyQP(y, cb, cr []uint8, width, height, qp int) ([]NALUnit, error) {
	if width <= 0 || height <= 0 || width&15 != 0 || height&15 != 0 ||
		len(y) != width*height || len(cb) != width*height/4 || len(cr) != width*height/4 || qp < 0 || qp > 51 {
		return nil, ErrInvalid
	}

	h := encoderHeaders{
		width: width, height: height, levelIDC: pcmLevelIDC(width * height), deblockingDisabled: true,
		ctbLog2: 6, maxTrHierIntra: 2,
	}
	rbsp, err := lossySliceQP(y, cb, cr, width, height, qp)
	if err != nil {
		return nil, err
	}

	return []NALUnit{
		{Type: NALVPS, TemporalID: 0, RBSP: h.vps()},
		{Type: NALSPS, TemporalID: 0, RBSP: h.sps()},
		{Type: NALPPS, TemporalID: 0, RBSP: h.pps()},
		{Type: NALIdrNLP, TemporalID: 0, RBSP: rbsp},
	}, nil
}

func lossySlice(y, cb, cr []uint8, width, height int) ([]byte, error) {
	return lossySliceQP(y, cb, cr, width, height, 26)
}

func lossySliceQP(y, cb, cr []uint8, width, height, qp int) ([]byte, error) {
	rbsp, _, _, _, err := lossySliceReconQP(y, cb, cr, width, height, qp)

	return rbsp, err
}

func lossySliceRecon(y, cb, cr []uint8, width, height int) ([]byte, []uint8, []uint8, []uint8, error) {
	return lossySliceReconQP(y, cb, cr, width, height, 26)
}

func lossySliceReconQP(y, cb, cr []uint8, width, height, qp int) ([]byte, []uint8, []uint8, []uint8, error) {
	var bits putBits
	bits.bit(1)
	bits.bit(0)
	bits.ue(0)
	bits.ue(uint32(sliceI))
	bits.se(int32(qp - 26))
	bits.rbspTrailingBits()

	s := &sps{chromaFormatIDC: 1}
	p := &pps{}
	var cabac cabacWriter
	cabac.init(&bits, int32(qp), sliceI, false)
	qpCb := int(chromaQP(int32(qp), 1))
	qpCr := qpCb

	reconY := make([]uint8, len(y))
	reconCb := make([]uint8, len(cb))
	reconCr := make([]uint8, len(cr))
	var stat [4]uint8
	modes := make([]int, width/16*height/16)
	depth := make([]uint8, len(modes))
	coded8 := make([]uint8, width/8*height/8)
	coded4 := make([]uint8, width/4*height/4)
	var modeScratch, yScratch, cbScratch, crScratch lossyBlockScratch
	blocksWide := width / 16
	blocksWide8 := width / 8

	markCoded8 := func(x, y int) {
		for j := range 2 {
			for i := range 2 {
				coded8[(y/8+j)*blocksWide8+x/8+i] = 1
			}
		}
	}
	blocksWide4 := width / 4

	markCoded4 := func(x, y, size int) {
		for j := range size / 4 {
			for i := range size / 4 {
				coded4[(y/4+j)*blocksWide4+x/4+i] = 1
			}
		}
	}

	encodeLeaf := func(x0, y0, d int) error {
		cand := lossyMPM(modes, blocksWide, x0/16, y0/16, 4)
		mode := lossyLumaModeRDOWithScratch(reconY, y, width, x0, y0, depth, cand, &cabac, qp, &modeScratch)
		var tus [4]lossyTU8Plan

		for i := range 4 {
			x, yy := x0+i&1*8, y0+i>>1*8
			var base, splitRecon, unsplitRecon [8 * 8]uint8
			for j := range 8 {
				copy(base[j*8:], reconY[(yy+j)*width+x:][:8])
			}
			idx8 := yy/8*blocksWide8 + x/8
			var idx4 [4]int
			for j := range 2 {
				for k := range 2 {
					idx4[j*2+k] = (yy/4+j)*blocksWide4 + x/4 + k
				}
			}
			base8 := coded8[idx8]
			var base4 [4]uint8
			for j, idx := range idx4 {
				base4[j] = coded4[idx]
			}

			restore := func() {
				for j := range 8 {
					copy(reconY[(yy+j)*width+x:][:8], base[j*8:])
				}
				coded8[idx8] = base8
				for j, idx := range idx4 {
					coded4[idx] = base4[j]
				}
			}
			distortion := func() int64 {
				var dist int64
				for j := range 8 {
					for k := range 8 {
						delta := int64(y[(yy+j)*width+x+k]) - int64(reconY[(yy+j)*width+x+k])
						dist += delta * delta
					}
				}
				return dist
			}

			var split lossyTU8Plan
			split.split = true
			for j := range 4 {
				px, py := x+j&1*4, yy+j>>1*4
				copy(split.y[j][:], lossyBlockTransformCodedWithScratch(reconY, y, width, px, py, 4, mode, 0, coded4, qp, true, &yScratch))
			}
			coded8[idx8] = 1
			splitDist := distortion()
			for j := range 8 {
				copy(splitRecon[j*8:], reconY[(yy+j)*width+x:][:8])
			}
			restore()

			var unsplit lossyTU8Plan
			copy(unsplit.y8[:], lossyBlockTransformCodedWithScratch(reconY, y, width, x, yy, 8, mode, 0, coded8, qp, false, &yScratch))
			for _, idx := range idx4 {
				coded4[idx] = 1
			}
			unsplitDist := distortion()
			for j := range 8 {
				copy(unsplitRecon[j*8:], reconY[(yy+j)*width+x:][:8])
			}

			chosen, chosenRecon := split, splitRecon
			if unsplitDist <= splitDist {
				chosen, chosenRecon = unsplit, unsplitRecon
			}
			for j := range 8 {
				copy(reconY[(yy+j)*width+x:][:8], chosenRecon[j*8:])
			}
			coded8[idx8] = 1
			for _, idx := range idx4 {
				coded4[idx] = 1
			}
			tus[i] = chosen
			copy(tus[i].cb[:], lossyBlockCodedWithScratch(reconCb, cb, width/2, x/2, yy/2, 4, mode, 1, coded8, qpCb, &cbScratch))
			copy(tus[i].cr[:], lossyBlockCodedWithScratch(reconCr, cr, width/2, x/2, yy/2, 4, mode, 2, coded8, qpCr, &crScratch))
			tus[i].cbfCb = hasCoefficients(tus[i].cb[:])
			tus[i].cbfCr = hasCoefficients(tus[i].cr[:])
		}

		rootCb, rootCr := false, false
		for i := range 4 {
			rootCb = rootCb || tus[i].cbfCb
			rootCr = rootCr || tus[i].cbfCr
		}

		cabac.encodeBin(ctxPartMode, 1)
		lossyIntraLumaMode(&cabac, mode, cand)
		modes[y0/16*width/16+x0/16] = mode
		cabac.encodeBin(ctxIntraChromaPredMode, 0)
		cabac.encodeBin(ctxSplitTransformFlag+1, 1)
		cabac.encodeBin(ctxCBFCBCR, boolToBit(rootCb))
		cabac.encodeBin(ctxCBFCBCR, boolToBit(rootCr))

		for i := range 4 {
			cabac.encodeBin(ctxSplitTransformFlag+2, boolToBit(tus[i].split))
			if rootCb {
				cabac.encodeBin(ctxCBFCBCR+1, boolToBit(tus[i].cbfCb))
			}
			if rootCr {
				cabac.encodeBin(ctxCBFCBCR+1, boolToBit(tus[i].cbfCr))
			}

			if tus[i].split {
				for j := range 4 {
					cbfY := hasCoefficients(tus[i].y[j][:])
					cabac.encodeBin(ctxCBFLuma, boolToBit(cbfY))
					if cbfY {
						if err := encodeResidual(&cabac, s, p, nil, tus[i].y[j][:],
							residualBlock{log2Size: 2, predModeIntra: mode, intra: true}, &stat); err != nil {
							return err
						}
					}
				}
			} else {
				cbfY := hasCoefficients(tus[i].y8[:])
				cabac.encodeBin(ctxCBFLuma, boolToBit(cbfY))
				if cbfY {
					if err := encodeResidual(&cabac, s, p, nil, tus[i].y8[:],
						residualBlock{log2Size: 3, predModeIntra: mode, intra: true}, &stat); err != nil {
						return err
					}
				}
			}
			if tus[i].cbfCb {
				if err := encodeResidual(&cabac, s, p, nil, tus[i].cb[:],
					residualBlock{log2Size: 2, cIdx: 1, predModeIntra: mode, intra: true}, &stat); err != nil {
					return err
				}
			}
			if tus[i].cbfCr {
				if err := encodeResidual(&cabac, s, p, nil, tus[i].cr[:],
					residualBlock{log2Size: 2, cIdx: 2, predModeIntra: mode, intra: true}, &stat); err != nil {
					return err
				}
			}
		}

		modes[y0*blocksWide/16+x0/16] = mode
		depth[y0*blocksWide/16+x0/16] = uint8(d)

		return nil
	}

	encodeCU32 := func(x0, y0, d int) error {
		cand := lossyMPM(modes, blocksWide, x0/16, y0/16, 4)
		mode := lossyLumaModeRDOWithScratch(reconY, y, width, x0, y0, depth, cand, &cabac, qp, &modeScratch)
		var qY [4][16 * 16]int32
		var qCb, qCr [4][8 * 8]int32

		for i := range 4 {
			x, yy := x0+i&1*16, y0+i>>1*16
			copy(qY[i][:], lossyBlockCodedWithScratch(reconY, y, width, x, yy, 16, mode, 0, depth, qp, &yScratch))
			markCoded8(x, yy)
			markCoded4(x, yy, 16)
			copy(qCb[i][:], lossyBlockCodedWithScratch(reconCb, cb, width/2, x/2, yy/2, 8, mode, 1, depth, qpCb, &cbScratch))
			copy(qCr[i][:], lossyBlockCodedWithScratch(reconCr, cr, width/2, x/2, yy/2, 8, mode, 2, depth, qpCr, &crScratch))
		}

		cbfCb, cbfCr := false, false
		for i := range 4 {
			cbfCb = cbfCb || hasCoefficients(qCb[i][:])
			cbfCr = cbfCr || hasCoefficients(qCr[i][:])
		}

		lossyIntraLumaMode(&cabac, mode, cand)
		cabac.encodeBin(ctxIntraChromaPredMode, 0)
		cabac.encodeBin(ctxCBFCBCR, boolToBit(cbfCb))
		cabac.encodeBin(ctxCBFCBCR, boolToBit(cbfCr))

		for i := range 4 {
			cbfCbLeaf := hasCoefficients(qCb[i][:])
			cbfCrLeaf := hasCoefficients(qCr[i][:])
			cabac.encodeBin(ctxSplitTransformFlag+1, 0)
			if cbfCb {
				cabac.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCbLeaf))
			}
			if cbfCr {
				cabac.encodeBin(ctxCBFCBCR+1, boolToBit(cbfCrLeaf))
			}
			cabac.encodeBin(ctxCBFLuma, boolToBit(hasCoefficients(qY[i][:])))

			if hasCoefficients(qY[i][:]) {
				if err := encodeResidual(&cabac, s, p, nil, qY[i][:],
					residualBlock{log2Size: 4, predModeIntra: mode, intra: true}, &stat); err != nil {
					return err
				}
			}
			if cbfCbLeaf {
				if err := encodeResidual(&cabac, s, p, nil, qCb[i][:],
					residualBlock{log2Size: 3, cIdx: 1, predModeIntra: mode, intra: true}, &stat); err != nil {
					return err
				}
			}
			if cbfCrLeaf {
				if err := encodeResidual(&cabac, s, p, nil, qCr[i][:],
					residualBlock{log2Size: 3, cIdx: 2, predModeIntra: mode, intra: true}, &stat); err != nil {
					return err
				}
			}
		}

		for j := range 2 {
			for i := range 2 {
				idx := (y0/16+j)*blocksWide + x0/16 + i
				modes[idx] = mode
				depth[idx] = uint8(d)
			}
		}

		return nil
	}

	var encodeTree func(int, int, int, int) error
	encodeTree = func(x0, y0, log2Size, d int) error {
		size := 1 << log2Size
		full := x0+size <= width && y0+size <= height
		if log2Size == 5 && full {
			ctx := 0
			if x0 > 0 && int(depth[y0/16*blocksWide+(x0-1)/16]) > d {
				ctx++
			}
			if y0 > 0 && int(depth[(y0-1)/16*blocksWide+x0/16]) > d {
				ctx++
			}
			cabac.encodeBin(ctxSplitCodingUnitFlag+ctx, 0)

			return encodeCU32(x0, y0, d)
		}
		if log2Size == 4 {
			return encodeLeaf(x0, y0, d)
		}
		if full {
			ctx := 0
			if x0 > 0 && int(depth[y0/16*blocksWide+(x0-1)/16]) > d {
				ctx++
			}
			if y0 > 0 && int(depth[(y0-1)/16*blocksWide+x0/16]) > d {
				ctx++
			}
			cabac.encodeBin(ctxSplitCodingUnitFlag+ctx, 1)
		}

		half := size / 2
		for i := range 4 {
			x, y := x0+i&1*half, y0+i>>1*half
			if x >= width || y >= height {
				continue
			}
			if err := encodeTree(x, y, log2Size-1, d+1); err != nil {
				return err
			}
		}

		return nil
	}

	for y0 := 0; y0 < height; y0 += 64 {
		for x0 := 0; x0 < width; x0 += 64 {
			if err := encodeTree(x0, y0, 6, 0); err != nil {
				return nil, nil, nil, nil, err
			}
			cabac.encodeTerminate(boolToBit(x0+64 >= width && y0+64 >= height))
		}
	}

	return cabac.bytes(), reconY, reconCb, reconCr, nil
}

func lossyLumaMode(recon, src []uint8, stride, x, y int) int {
	var scratch lossyBlockScratch

	return lossyLumaModeWithScratch(recon, src, stride, x, y, &scratch)
}

func lossyLumaModeWithScratch(recon, src []uint8, stride, x, y int, scratch *lossyBlockScratch) int {
	return lossyLumaModeCodedWithScratch(recon, src, stride, x, y, nil, 26, scratch)
}

func lossyLumaModeCodedWithScratch(recon, src []uint8, stride, x, y int, coded []uint8,
	qp int, scratch *lossyBlockScratch,
) int {
	bestMode, bestDist := intraPlanar, int64(-1)
	for mode := intraPlanar; mode <= 34; mode++ {
		_, trial := lossyBlockDataWithScratch(recon, src, stride, x, y, 16, mode, 0, coded, qp, false, scratch)
		var dist int64
		for j := range 16 {
			for i := range 16 {
				d := int64(src[(y+j)*stride+x+i]) - int64(trial[j*16+i])
				dist += d * d
			}
		}
		if bestDist < 0 || dist < bestDist {
			bestMode, bestDist = mode, dist
		}
	}
	return bestMode
}

func lossyLumaModeRDOWithScratch(recon, src []uint8, stride, x, y int, coded []uint8, cand [3]int,
	cabac *cabacWriter, qp int, scratch *lossyBlockScratch,
) int {
	bestModes := [4]int{intraPlanar, intraPlanar, intraPlanar, intraPlanar}
	bestDist := [4]int64{1<<63 - 1, 1<<63 - 1, 1<<63 - 1, 1<<63 - 1}
	for mode := intraPlanar; mode <= 34; mode++ {
		_, trial := lossyBlockDataWithScratch(recon, src, stride, x, y, 16, mode, 0, coded, qp, false, scratch)
		var dist int64
		for j := range 16 {
			for i := range 16 {
				d := int64(src[(y+j)*stride+x+i]) - int64(trial[j*16+i])
				dist += d * d
			}
		}
		for i := range bestModes {
			if dist >= bestDist[i] {
				continue
			}
			copy(bestModes[i+1:], bestModes[i:len(bestModes)-1])
			copy(bestDist[i+1:], bestDist[i:len(bestDist)-1])
			bestModes[i], bestDist[i] = mode, dist
			break
		}
	}
	bestMode, bestCost := bestModes[0], int64(-1)
	lambda := lossyLambda(qp)
	for i, mode := range bestModes {
		coef, _ := lossyBlockDataWithScratch(recon, src, stride, x, y, 16, mode, 0, coded, qp, false, scratch)
		cost := bestDist[i] + lambda*lossyModeRate(cabac, cand, mode, coef, scratch)
		if bestCost < 0 || cost < bestCost {
			bestMode, bestCost = mode, cost
		}
	}

	return bestMode
}

func lossyModeRate(cabac *cabacWriter, cand [3]int, mode int, coef []int32, scratch *lossyBlockScratch) int64 {
	bits := putBits{data: scratch.rate[:0]}
	w := *cabac
	w.bits = &bits
	lossyIntraLumaMode(&w, mode, cand)
	cbf := hasCoefficients(coef)
	w.encodeBin(ctxCBFLuma, boolToBit(cbf))
	if cbf {
		_ = encodeResidual(&w, &sps{chromaFormatIDC: 1}, &pps{}, nil, coef,
			residualBlock{log2Size: 4, predModeIntra: mode, intra: true}, &[4]uint8{})
	}
	w.encodeTerminate(1)
	if bits.nbits == 0 {
		return int64(len(bits.data) * 8)
	}

	return int64((len(bits.data)-1)*8 + int(bits.nbits))
}

func lossyLambda(qp int) int64 {
	shift := max(qp-12, 0) / 3

	return max(int64(1), int64(9<<shift)/16)
}

func lossyBlock(recon, src []uint8, stride, x, y, n, mode, cIdx int) []int32 {
	var scratch lossyBlockScratch

	return lossyBlockWithScratch(recon, src, stride, x, y, n, mode, cIdx, &scratch)
}

func lossyBlockWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int,
	scratch *lossyBlockScratch,
) []int32 {
	return lossyBlockCodedWithScratch(recon, src, stride, x, y, n, mode, cIdx, nil, 26, scratch)
}

func lossyBlockCodedWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int, coded []uint8,
	qp int, scratch *lossyBlockScratch,
) []int32 {
	return lossyBlockTransformCodedWithScratch(recon, src, stride, x, y, n, mode, cIdx, coded, qp, false, scratch)
}

func lossyBlockTransformCodedWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int, coded []uint8,
	qp int, dst bool, scratch *lossyBlockScratch,
) []int32 {
	coef, block := lossyBlockDataWithScratch(recon, src, stride, x, y, n, mode, cIdx, coded, qp, dst, scratch)
	for j := range n {
		copy(recon[(y+j)*stride+x:], block[j*n:(j+1)*n])
	}
	if coded != nil {
		coded[y/n*(stride/n)+x/n] = 1
	}
	return coef
}

type lossyBlockScratch struct {
	pred                      []uint8
	avail                     []bool
	residual, coef, reconCoef []int32
	rate                      [512]byte
	ref                       refSamples
	transform                 transformScratch
}

type lossyTU8Plan struct {
	split        bool
	y            [4][4 * 4]int32
	y8           [8 * 8]int32
	cb, cr       [4 * 4]int32
	cbfCb, cbfCr bool
}

func lossyBlockDataWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int,
	coded []uint8, qp int, dst bool,
	scratch *lossyBlockScratch,
) ([]int32, []uint8) {
	count := n * n
	if cap(scratch.pred) < count {
		scratch.pred = make([]uint8, count)
	}
	if cap(scratch.avail) < 4*n+1 {
		scratch.avail = make([]bool, 4*n+1)
	}
	if cap(scratch.residual) < count {
		scratch.residual = make([]int32, count)
	}
	if cap(scratch.coef) < count {
		scratch.coef = make([]int32, count)
	}
	if cap(scratch.reconCoef) < count {
		scratch.reconCoef = make([]int32, count)
	}

	pred := scratch.pred[:count]
	avail := scratch.avail[:4*n+1]
	residual := scratch.residual[:count]
	coef := scratch.coef[:count]
	reconCoef := scratch.reconCoef[:count]

	lossyPrediction(recon, stride, x, y, n, mode, cIdx, coded, pred, avail, &scratch.ref)
	for j := range n {
		for i := range n {
			residual[j*n+i] = int32(src[(y+j)*stride+x+i]) - int32(pred[j*n+i])
		}
	}

	if dst {
		forwardTransformDST4(coef, residual, 8)
	} else {
		forwardTransform(coef, residual, n, 8)
	}
	quantize(coef, coef, n, qp, 8)

	copy(reconCoef, coef)
	dequant(reconCoef, nil, n, qp, 8, false)
	inverseTransform(reconCoef, n, dst, 8, false, &scratch.transform)
	addResidual(pred, n, 0, 0, n, residualShiftBits(8, false), reconCoef, 8)

	return coef, pred
}

func lossyPrediction(recon []uint8, stride, x, y, n, mode, cIdx int, coded []uint8, out []uint8, avail []bool,
	ref *refSamples,
) {
	ref.n = n
	clear(avail)
	blocksWide := stride / n
	block := y/n*blocksWide + x/n
	for i := range 4*n + 1 {
		var nx, ny int
		switch {
		case i < 2*n:
			nx, ny = x-1, y+2*n-1-i
		case i == 2*n:
			nx, ny = x-1, y-1
		default:
			nx, ny = x+i-2*n-1, y-1
		}

		ok := nx >= 0 && ny >= 0 && nx < stride && ny < len(recon)/stride
		if ok {
			if coded != nil {
				ok = coded[ny/n*blocksWide+nx/n] != 0
			} else {
				ok = ny/n*blocksWide+nx/n < block
			}
		}
		if ok {
			ref.s[i] = int32(recon[ny*stride+nx])
			avail[i] = true
		}
	}
	ref.substitute(avail, 8)
	filterRef(ref, mode, cIdx, 8, &sps{chromaFormatIDC: 1})
	intraPredict(out, 0, n, ref, mode, cIdx, 8)
}

func lossyMPM(modes []int, blocksWide, x, y, ctbBlocks int) [3]int {
	candA, candB := intraDC, intraDC
	if x > 0 {
		candA = modes[y*blocksWide+x-1]
	}
	if y > 0 && y%ctbBlocks != 0 {
		candB = modes[(y-1)*blocksWide+x]
	}
	var cand [3]int
	switch {
	case candA == candB && candA < 2:
		cand = [3]int{intraPlanar, intraDC, intraVer}
	case candA == candB:
		cand = [3]int{candA, 2 + (candA+29)%32, 2 + (candA-2+1)%32}
	default:
		cand[0], cand[1] = candA, candB
		switch {
		case candA != intraPlanar && candB != intraPlanar:
			cand[2] = intraPlanar
		case candA != intraDC && candB != intraDC:
			cand[2] = intraDC
		default:
			cand[2] = intraVer
		}
	}
	return cand
}

func lossyIntraLumaMode(w *cabacWriter, mode int, cand [3]int) {
	for i, m := range cand {
		if mode != m {
			continue
		}
		w.encodeBin(ctxPrevIntraLumaPredFlag, 1)
		if i == 0 {
			w.encodeBypass(0)
		} else {
			w.encodeBypass(1)
			w.encodeBypass(uint32(i - 1))
		}
		return
	}
	w.encodeBin(ctxPrevIntraLumaPredFlag, 0)
	for i := 0; i < len(cand); i++ {
		for j := i + 1; j < len(cand); j++ {
			if cand[j] < cand[i] {
				cand[i], cand[j] = cand[j], cand[i]
			}
		}
	}
	rem := mode
	for _, m := range cand {
		if mode > m {
			rem--
		}
	}
	w.encodeBypassBits(uint32(rem), 5)
}

func hasCoefficients(coef []int32) bool {
	for _, v := range coef {
		if v != 0 {
			return true
		}
	}

	return false
}
