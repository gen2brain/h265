package hevc

import "testing"

func TestDiagScan(t *testing.T) {
	wantX := []uint8{0, 0, 1, 0, 1, 2, 0, 1, 2, 3, 1, 2, 3, 2, 3, 3}
	wantY := []uint8{0, 1, 0, 2, 1, 0, 3, 2, 1, 0, 3, 2, 1, 3, 2, 3}

	if len(scanOrder[2][scanDiag]) != 16 {
		t.Fatalf("len = %d", len(scanOrder[2][scanDiag]))
	}

	for i, p := range scanOrder[2][scanDiag] {
		if p.x != wantX[i] || p.y != wantY[i] {
			t.Fatalf("scan[%d] = (%d,%d), want (%d,%d)", i, p.x, p.y, wantX[i], wantY[i])
		}
	}

	if len(scanOrder[3][scanDiag]) != 64 {
		t.Fatalf("len = %d", len(scanOrder[3][scanDiag]))
	}

	seen := make(map[scanPos]bool, 64)
	for _, p := range scanOrder[3][scanDiag] {
		if p.x > 7 || p.y > 7 || seen[p] {
			t.Fatalf("bad 8x8 scan entry (%d,%d)", p.x, p.y)
		}

		seen[p] = true
	}
}

func TestScanOrdersArePermutations(t *testing.T) {
	for k := range scanOrder {
		size := 1 << k

		for idx := range nScanOrders {
			scan := scanOrder[k][idx]

			if len(scan) != size*size {
				t.Fatalf("log2=%d idx=%d has %d entries, want %d", k, idx, len(scan), size*size)
			}

			seen := make(map[scanPos]bool, len(scan))

			for _, p := range scan {
				if int(p.x) >= size || int(p.y) >= size {
					t.Fatalf("log2=%d idx=%d visits (%d,%d) outside the block", k, idx, p.x, p.y)
				}

				if seen[p] {
					t.Fatalf("log2=%d idx=%d visits (%d,%d) twice", k, idx, p.x, p.y)
				}

				seen[p] = true
			}
		}
	}
}

func TestScanHorVerTranspose(t *testing.T) {
	for k := range scanOrder {
		hor, ver := scanOrder[k][scanHor], scanOrder[k][scanVer]

		for i := range hor {
			if hor[i].x != ver[i].y || hor[i].y != ver[i].x {
				t.Fatalf("log2=%d [%d]: horizontal (%d,%d) is not the transpose of vertical (%d,%d)",
					k, i, hor[i].x, hor[i].y, ver[i].x, ver[i].y)
			}
		}
	}
}

// TestDiagScanOrdering checks the up-right property: the anti-diagonal index
// never decreases, and within one it moves up and to the right.
func TestDiagScanOrdering(t *testing.T) {
	for k := range scanOrder {
		scan := scanOrder[k][scanDiag]

		for i := 1; i < len(scan); i++ {
			prev, cur := scan[i-1], scan[i]

			pd, cd := int(prev.x)+int(prev.y), int(cur.x)+int(cur.y)

			if cd < pd {
				t.Fatalf("log2=%d [%d]: diagonal went from %d back to %d", k, i, pd, cd)
			}

			if cd == pd && cur.x <= prev.x {
				t.Fatalf("log2=%d [%d]: within diagonal %d, x went from %d to %d",
					k, i, cd, prev.x, cur.x)
			}
		}
	}
}
