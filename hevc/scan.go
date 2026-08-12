package hevc

type scanPos struct {
	x, y uint8
}

const (
	scanDiag = iota
	scanHor
	scanVer
	nScanOrders
)

const maxScanLog2 = 5

func buildDiagScan(size int) []scanPos {
	scan := make([]scanPos, 0, size*size)

	for d := range 2*size - 1 {
		for y := min(d, size-1); y >= max(0, d-size+1); y-- {
			scan = append(scan, scanPos{uint8(d - y), uint8(y)})
		}
	}

	return scan
}

func buildHorScan(size int) []scanPos {
	scan := make([]scanPos, 0, size*size)

	for y := range size {
		for x := range size {
			scan = append(scan, scanPos{uint8(x), uint8(y)})
		}
	}

	return scan
}

func buildVerScan(size int) []scanPos {
	scan := make([]scanPos, 0, size*size)

	for x := range size {
		for y := range size {
			scan = append(scan, scanPos{uint8(x), uint8(y)})
		}
	}

	return scan
}

// scanOrder is ScanOrder[log2BlockSize][scanIdx] of 6.5.
var scanOrder [maxScanLog2 + 1][nScanOrders][]scanPos

func init() {
	for k := range scanOrder {
		size := 1 << k

		scanOrder[k][scanDiag] = buildDiagScan(size)
		scanOrder[k][scanHor] = buildHorScan(size)
		scanOrder[k][scanVer] = buildVerScan(size)
	}
}
