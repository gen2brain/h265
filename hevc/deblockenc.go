package hevc

// edgeV and edgeH mark a 4x4 block as the first column, or the first row, of a
// transform block, which a coding unit's own edges are always among.
const (
	edgeV = 1 << iota
	edgeH
)

// markTU records where a transform block begins, at the granularity 8.7.2
// reads its edges in.
func (e *intraEncoder[P]) markTU(x, y, n int) {
	bw := e.width / 4

	for j := range n / 4 {
		row := e.edges[(y/4+j)*bw+x/4:][:n/4]

		if j == 0 {
			for i := range row {
				row[i] |= edgeH
			}
		}

		row[0] |= edgeV
	}
}

// deblockRecon is 8.7.2 over the reconstruction: every vertical edge and then
// every horizontal one, on the eight-sample grid. It runs once the picture is
// whole, which 8.4.4.2.2 predicts from the samples before.
func (e *intraEncoder[P]) deblockRecon() {
	var hdr sliceHeader

	for _, vertical := range []bool{true, false} {
		for y := 0; y < e.height; y += 8 {
			if !vertical && y == 0 {
				continue
			}

			for x := 0; x < e.width; x += 8 {
				if vertical && x == 0 {
					continue
				}

				e.deblockEdge(x, y, vertical, &hdr)
			}
		}
	}
}

// deblockEdge filters the two four-sample segments of one edge. Every coding
// unit here is intra, so 8.7.2.4 gives each of them strength two.
func (e *intraEncoder[P]) deblockEdge(x, y int, vertical bool, hdr *sliceHeader) {
	bw := e.width / 4
	sw, sh := e.shiftW, e.shiftH

	step, line := 1, e.width
	if !vertical {
		step, line = e.width, 1
	}

	base := y*e.width + x

	beta, tc := betaTc(int32(e.qp), 2, hdr, e.bitDepth)

	var plan [2]lumaPlan

	for k := 0; k < 8; k += 4 {
		qx, qy := x, y+k
		if !vertical {
			qx, qy = x+k, y
		}

		if qx >= e.width || qy >= e.height {
			continue
		}

		mask := uint8(edgeV)
		if !vertical {
			mask = edgeH
		}

		if e.edges[qy/4*bw+qx/4]&mask == 0 {
			continue
		}

		if beta != 0 && tc != 0 {
			plan[k>>2] = deblockLumaPlan(e.recon[0], base+k*line, line, step, beta, tc)
		}

		if e.s.chromaArrayType() == 0 {
			continue
		}

		if (vertical && x&(8<<sw-1) != 0) || (!vertical && y&(8<<sh-1) != 0) {
			continue
		}

		n := 4 >> sh
		if !vertical {
			n = 4 >> sw
		}

		for c := 1; c <= 2; c++ {
			deblockChroma(e.recon[c], e.strideC, qx>>sw, qy>>sh, n, vertical,
				int32(e.qpDeblockC), hdr, e.bitDepth, false, false)
		}
	}

	deblockLumaEdge(e.recon[0], base, line, step, plan, e.bitDepth,
		[2]bool{}, [2]bool{})
}
