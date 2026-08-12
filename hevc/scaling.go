package hevc

var defaultScalingListIntra = [64]uint8{
	16, 16, 16, 16, 17, 18, 21, 24,
	16, 16, 16, 16, 17, 19, 22, 25,
	16, 16, 17, 18, 20, 22, 25, 29,
	16, 16, 18, 21, 24, 27, 31, 36,
	17, 17, 20, 24, 30, 35, 41, 47,
	18, 19, 22, 27, 35, 44, 54, 65,
	21, 22, 25, 31, 41, 54, 70, 88,
	24, 25, 29, 36, 47, 65, 88, 115,
}

var defaultScalingListInter = [64]uint8{
	16, 16, 16, 16, 17, 18, 20, 24,
	16, 16, 16, 17, 18, 20, 24, 25,
	16, 16, 17, 18, 20, 24, 25, 28,
	16, 17, 18, 20, 24, 25, 28, 33,
	17, 18, 20, 24, 25, 28, 33, 41,
	18, 20, 24, 25, 28, 33, 41, 54,
	20, 24, 25, 28, 33, 41, 54, 71,
	24, 25, 28, 33, 41, 54, 71, 91,
}

func defaultScalingList() scalingList {
	var sl scalingList

	for m := range sl.sl[0] {
		for i := range 16 {
			sl.sl[0][m][i] = 16
		}
	}

	for s := 1; s < maxScalingListSizes; s++ {
		for m := range sl.sl[s] {
			if m < 3 {
				sl.sl[s][m] = defaultScalingListIntra
			} else {
				sl.sl[s][m] = defaultScalingListInter
			}
		}
	}

	for s := range sl.dc {
		for m := range sl.dc[s] {
			sl.dc[s][m] = 16
		}
	}

	return sl
}

func parseScalingListData(c *getBits, sl *scalingList) error {
	for sizeID := range maxScalingListSizes {
		step := 1
		if sizeID == 3 {
			step = 3
		}

		for matrixID := 0; matrixID < maxScalingListMats; matrixID += step {
			if c.bit() == 0 {
				delta := int(c.ue()) * step
				if delta > matrixID {
					return errInvalid
				}

				if delta == 0 {
					continue
				}

				ref := matrixID - delta
				sl.sl[sizeID][matrixID] = sl.sl[sizeID][ref]

				if sizeID > 1 {
					sl.dc[sizeID-2][matrixID] = sl.dc[sizeID-2][ref]
				}

				continue
			}

			next := int32(8)

			if sizeID > 1 {
				dc := c.se()
				if dc < -7 || dc > 247 {
					return errInvalid
				}

				next = dc + 8
				sl.dc[sizeID-2][matrixID] = uint8(next)
			}

			scan, size := scanOrder[3][scanDiag], 8
			if sizeID == 0 {
				scan, size = scanOrder[2][scanDiag], 4
			}

			for _, p := range scan {
				d := c.se()
				if d < -128 || d > 127 {
					return errInvalid
				}

				next = (next + d + 256) % 256
				sl.sl[sizeID][matrixID][int(p.y)*size+int(p.x)] = uint8(next)
			}
		}
	}

	if c.err {
		return errInvalid
	}

	return nil
}

// factors is the ScalingFactor derivation of 7.4.5, replicating the eight by
// eight lists across the larger transform sizes and overriding the DC term.
func (sl *scalingList) factors() [maxScalingListSizes][maxScalingListMats][]uint8 {
	var out [maxScalingListSizes][maxScalingListMats][]uint8

	for sizeID := range maxScalingListSizes {
		n := 4 << sizeID
		rep := n / 8

		for matrixID := range maxScalingListMats {
			f := make([]uint8, n*n)

			if sizeID == 0 {
				copy(f, sl.sl[0][matrixID][:16])
				out[sizeID][matrixID] = f

				continue
			}

			for y := range n {
				for x := range n {
					f[y*n+x] = sl.sl[sizeID][matrixID][y/rep*8+x/rep]
				}
			}

			if sizeID > 1 {
				f[0] = sl.dc[sizeID-2][matrixID]
			}

			out[sizeID][matrixID] = f
		}
	}

	return out
}
