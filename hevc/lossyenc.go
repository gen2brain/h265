package hevc

func encodeIntraLossy(y, cb, cr []uint8, width, height int) ([]NALUnit, error) {
	if width <= 0 || height <= 0 || width&15 != 0 || height&15 != 0 ||
		len(y) != width*height || len(cb) != width*height/4 || len(cr) != width*height/4 {
		return nil, ErrInvalid
	}

	h := encoderHeaders{
		width: width, height: height, levelIDC: pcmLevelIDC(width * height), deblockingDisabled: true,
		ctbLog2: 6,
	}
	rbsp, err := lossySlice(y, cb, cr, width, height)
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
	rbsp, _, _, _, err := lossySliceRecon(y, cb, cr, width, height)

	return rbsp, err
}

func lossySliceRecon(y, cb, cr []uint8, width, height int) ([]byte, []uint8, []uint8, []uint8, error) {
	var bits putBits
	bits.bit(1)
	bits.bit(0)
	bits.ue(0)
	bits.ue(uint32(sliceI))
	bits.se(0)
	bits.rbspTrailingBits()

	s := &sps{chromaFormatIDC: 1}
	p := &pps{}
	var cabac cabacWriter
	cabac.init(&bits, 26, sliceI, false)

	reconY := make([]uint8, len(y))
	reconCb := make([]uint8, len(cb))
	reconCr := make([]uint8, len(cr))
	var stat [4]uint8
	modes := make([]int, width/16*height/16)
	depth := make([]uint8, len(modes))
	var modeScratch, yScratch, cbScratch, crScratch lossyBlockScratch
	blocksWide := width / 16

	encodeLeaf := func(x0, y0 int) error {
		mode := lossyLumaModeCodedWithScratch(reconY, y, width, x0, y0, depth, &modeScratch)
		qY := lossyBlockCodedWithScratch(reconY, y, width, x0, y0, 16, mode, 0, depth, &yScratch)
		qCb := lossyBlockCodedWithScratch(reconCb, cb, width/2, x0/2, y0/2, 8, mode, 1, depth, &cbScratch)
		qCr := lossyBlockCodedWithScratch(reconCr, cr, width/2, x0/2, y0/2, 8, mode, 2, depth, &crScratch)

		cand := lossyMPM(modes, blocksWide, x0/16, y0/16, 4)

		cabac.encodeBin(ctxPartMode, 1)
		lossyIntraLumaMode(&cabac, mode, cand)
		modes[y0/16*width/16+x0/16] = mode
		cabac.encodeBin(ctxIntraChromaPredMode, 0)
		cabac.encodeBin(ctxCBFCBCR, boolToBit(hasCoefficients(qCb)))
		cabac.encodeBin(ctxCBFCBCR, boolToBit(hasCoefficients(qCr)))
		cabac.encodeBin(ctxCBFLuma+1, boolToBit(hasCoefficients(qY)))

		if hasCoefficients(qY) {
			if err := encodeResidual(&cabac, s, p, nil, qY,
				residualBlock{log2Size: 4, predModeIntra: mode, intra: true}, &stat); err != nil {
				return err
			}
		}
		if hasCoefficients(qCb) {
			if err := encodeResidual(&cabac, s, p, nil, qCb,
				residualBlock{log2Size: 3, cIdx: 1, predModeIntra: mode, intra: true}, &stat); err != nil {
				return err
			}
		}
		if hasCoefficients(qCr) {
			if err := encodeResidual(&cabac, s, p, nil, qCr,
				residualBlock{log2Size: 3, cIdx: 2, predModeIntra: mode, intra: true}, &stat); err != nil {
				return err
			}
		}

		modes[y0*blocksWide/16+x0/16] = mode
		depth[y0*blocksWide/16+x0/16] = 2

		return nil
	}

	var encodeTree func(int, int, int, int) error
	encodeTree = func(x0, y0, log2Size, d int) error {
		if log2Size == 4 {
			return encodeLeaf(x0, y0)
		}

		size := 1 << log2Size
		if x0+size <= width && y0+size <= height {
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
	return lossyLumaModeCodedWithScratch(recon, src, stride, x, y, nil, scratch)
}

func lossyLumaModeCodedWithScratch(recon, src []uint8, stride, x, y int, coded []uint8,
	scratch *lossyBlockScratch,
) int {
	bestMode, bestDist := intraPlanar, int64(-1)
	for mode := intraPlanar; mode <= 34; mode++ {
		_, trial := lossyBlockDataWithScratch(recon, src, stride, x, y, 16, mode, 0, coded, scratch)
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

func lossyBlock(recon, src []uint8, stride, x, y, n, mode, cIdx int) []int32 {
	var scratch lossyBlockScratch

	return lossyBlockWithScratch(recon, src, stride, x, y, n, mode, cIdx, &scratch)
}

func lossyBlockWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int,
	scratch *lossyBlockScratch,
) []int32 {
	return lossyBlockCodedWithScratch(recon, src, stride, x, y, n, mode, cIdx, nil, scratch)
}

func lossyBlockCodedWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int, coded []uint8,
	scratch *lossyBlockScratch,
) []int32 {
	coef, block := lossyBlockDataWithScratch(recon, src, stride, x, y, n, mode, cIdx, coded, scratch)
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
	ref                       refSamples
	transform                 transformScratch
}

func lossyBlockDataWithScratch(recon, src []uint8, stride, x, y, n, mode, cIdx int,
	coded []uint8,
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

	forwardTransform(coef, residual, n, 8)
	quantize(coef, coef, n, 26, 8)

	copy(reconCoef, coef)
	dequant(reconCoef, nil, n, 26, 8, false)
	inverseTransform(reconCoef, n, false, 8, false, &scratch.transform)
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
