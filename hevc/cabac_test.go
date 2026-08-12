package hevc

import (
	"math/rand/v2"
	"testing"
)

// Table 9-45.
func transIdxMPS(state int) int {
	if state >= 62 {
		return state
	}

	return state + 1
}

func TestNormShift(t *testing.T) {
	for r := 1; r < 512; r++ {
		n := normShift(uint32(r))

		v := uint32(r) << n
		if v < 256 || v > 511 {
			t.Fatalf("normShift(%d) = %d gives %d", r, n, v)
		}

		if n > 0 && uint32(r)<<(n-1) >= 256 {
			t.Fatalf("normShift(%d) = %d overshoots", r, n)
		}
	}
}

func TestTransitionTables(t *testing.T) {
	for st := range 64 {
		for v := range 2 {
			s := 2*st + v

			wantMPS := 2*transIdxMPS(st) + v
			if got := int(transState[128+s]); got != wantMPS {
				t.Fatalf("MPS from state %d/%d = %d, want %d", st, v, got, wantMPS)
			}

			mps := v
			if st == 0 {
				mps = 1 - v
			}

			wantLPS := 2*int(transIdxLPS[st]) + mps
			if got := int(transState[127-s]); got != wantLPS {
				t.Fatalf("LPS from state %d/%d = %d, want %d", st, v, got, wantLPS)
			}
		}
	}

	for g := range 4 {
		for st := range 64 {
			for v := range 2 {
				if got := lpsRange[g][2*st+v]; got != rangeTabLPS[st][g] {
					t.Fatalf("lpsRange[%d][%d] = %d, want %d", g, 2*st+v, got, rangeTabLPS[st][g])
				}
			}
		}
	}
}

func TestInitState(t *testing.T) {
	clip := func(v, lo, hi int32) int32 {
		if v < lo {
			return lo
		}

		if v > hi {
			return hi
		}

		return v
	}

	for v := range 256 {
		for qp := int32(-10); qp <= 60; qp++ {
			iv := uint8(v)
			m := int32(iv>>4)*5 - 45
			n := int32(iv&0x0f)<<3 - 16

			pre := clip((m*clip(qp, 0, 51)>>4)+n, 1, 126)

			valMps := int32(0)
			pStateIdx := 63 - pre

			if pre > 63 {
				valMps = 1
				pStateIdx = pre - 64
			}

			want := uint8(2*pStateIdx + valMps)

			if got := initState(iv, qp); got != want {
				t.Fatalf("initState(%d, %d) = %d, want %d", iv, qp, got, want)
			}
		}
	}
}

func TestInitType(t *testing.T) {
	tests := []struct {
		t    sliceType
		flag bool
		want int
	}{
		{sliceI, false, 0},
		{sliceI, true, 0},
		{sliceP, false, 1},
		{sliceP, true, 2},
		{sliceB, false, 2},
		{sliceB, true, 1},
	}

	for _, tt := range tests {
		if got := initType(tt.t, tt.flag); got != tt.want {
			t.Fatalf("initType(%d, %v) = %d, want %d", tt.t, tt.flag, got, tt.want)
		}
	}
}

// The arithmetic encoder of 9.3.4.4, longhand.
type cabacEncoder struct {
	low         uint32
	rng         uint32
	outstanding int
	firstBit    bool
	out         []byte
	nbits       int

	pStateIdx [nContexts]uint8
	valMps    [nContexts]uint8
}

func newCabacEncoder(qp int32, st sliceType, cabacInit bool) *cabacEncoder {
	e := &cabacEncoder{low: 0, rng: 510, firstBit: true}

	row := &initValues[initType(st, cabacInit)]
	for i := range e.pStateIdx {
		s := initState(row[i], qp)
		e.pStateIdx[i] = s >> 1
		e.valMps[i] = s & 1
	}

	return e
}

func (e *cabacEncoder) writeBit(b uint32) {
	if e.nbits%8 == 0 {
		e.out = append(e.out, 0)
	}

	if b != 0 {
		e.out[len(e.out)-1] |= 1 << (7 - e.nbits%8)
	}

	e.nbits++
}

func (e *cabacEncoder) putBit(b uint32) {
	if e.firstBit {
		e.firstBit = false
	} else {
		e.writeBit(b)
	}

	for e.outstanding > 0 {
		e.writeBit(1 - b)
		e.outstanding--
	}
}

func (e *cabacEncoder) renorm() {
	for e.rng < 256 {
		switch {
		case e.low < 256:
			e.putBit(0)
		case e.low >= 512:
			e.low -= 512
			e.putBit(1)
		default:
			e.low -= 256
			e.outstanding++
		}

		e.rng <<= 1
		e.low <<= 1
	}
}

func (e *cabacEncoder) encodeBin(ctx int, bin uint32) {
	lps := uint32(rangeTabLPS[e.pStateIdx[ctx]][e.rng>>6&3])
	e.rng -= lps

	if bin != uint32(e.valMps[ctx]) {
		e.low += e.rng
		e.rng = lps

		if e.pStateIdx[ctx] == 0 {
			e.valMps[ctx] = 1 - e.valMps[ctx]
		}

		e.pStateIdx[ctx] = transIdxLPS[e.pStateIdx[ctx]]
	} else {
		e.pStateIdx[ctx] = uint8(transIdxMPS(int(e.pStateIdx[ctx])))
	}

	e.renorm()
}

func (e *cabacEncoder) encodeBypass(bin uint32) {
	e.low <<= 1
	if bin != 0 {
		e.low += e.rng
	}

	switch {
	case e.low >= 1024:
		e.low -= 1024
		e.putBit(1)
	case e.low < 512:
		e.putBit(0)
	default:
		e.low -= 512
		e.outstanding++
	}
}

func (e *cabacEncoder) encodeTerminate(bin uint32) {
	e.rng -= 2

	if bin == 0 {
		e.renorm()

		return
	}

	e.low += e.rng
	e.rng = 2
	e.renorm()

	e.putBit(e.low >> 9 & 1)
	e.writeBit(e.low >> 8 & 1)
	e.writeBit(1)
}

func (e *cabacEncoder) finish() []byte {
	for len(e.out) < 8 {
		e.out = append(e.out, 0)
	}

	return e.out
}

type binOp struct {
	kind int
	ctx  int
	val  uint32
	n    int
}

const (
	opBin = iota
	opBypass
	opBypassBits
	opTerminate
)

func TestCABACRoundTrip(t *testing.T) {
	for _, st := range []sliceType{sliceI, sliceP, sliceB} {
		for _, qp := range []int32{0, 1, 12, 26, 37, 51} {
			for seed := range uint64(24) {
				r := rand.New(rand.NewPCG(seed, uint64(qp)))

				ops := make([]binOp, 0, 4096)
				for range 3000 + r.IntN(1000) {
					switch r.IntN(10) {
					case 0, 1:
						ops = append(ops, binOp{kind: opBypass, val: uint32(r.IntN(2))})
					case 2:
						n := 1 + r.IntN(16)
						ops = append(ops, binOp{
							kind: opBypassBits,
							n:    n,
							val:  uint32(r.Uint64() & (1<<uint(n) - 1)),
						})
					case 3:
						ops = append(ops, binOp{kind: opTerminate, val: 0})
					default:
						ops = append(ops, binOp{
							kind: opBin,
							ctx:  r.IntN(nContexts),
							val:  uint32(r.IntN(2)),
						})
					}
				}

				e := newCabacEncoder(qp, st, false)

				for _, op := range ops {
					switch op.kind {
					case opBin:
						e.encodeBin(op.ctx, op.val)
					case opBypass:
						e.encodeBypass(op.val)
					case opBypassBits:
						for i := op.n - 1; i >= 0; i-- {
							e.encodeBypass(op.val >> uint(i) & 1)
						}
					case opTerminate:
						e.encodeTerminate(0)
					}
				}

				e.encodeTerminate(1)

				var d cabac
				if err := d.init(e.finish(), 0); err != nil {
					t.Fatalf("qp %d seed %d: %v", qp, seed, err)
				}

				d.initContexts(qp, st, false)

				for i, op := range ops {
					var got uint32

					switch op.kind {
					case opBin:
						got = d.decodeBin(op.ctx)
					case opBypass:
						got = d.decodeBypass()
					case opBypassBits:
						got = d.decodeBypassBits(op.n)
					case opTerminate:
						got = d.decodeTerminate()
					}

					if got != op.val {
						t.Fatalf("slice %d qp %d seed %d: op %d kind %d: got %d, want %d",
							st, qp, seed, i, op.kind, got, op.val)
					}
				}

				if got := d.decodeTerminate(); got != 1 {
					t.Fatalf("slice %d qp %d seed %d: final terminate = %d", st, qp, seed, got)
				}
			}
		}
	}
}

func TestCABACInitBounds(t *testing.T) {
	var c cabac

	if err := c.init([]byte{0x01}, 0); err == nil {
		t.Fatal("short buffer accepted")
	}

	if err := c.init([]byte{0x01, 0x02}, 1); err == nil {
		t.Fatal("offset past end accepted")
	}

	if err := c.init([]byte{0x01, 0x02}, -1); err == nil {
		t.Fatal("negative offset accepted")
	}

	if err := c.init([]byte{0x01, 0x02}, 0); err != nil {
		t.Fatalf("valid init: %v", err)
	}

	if c.rng != 0x1fe {
		t.Fatalf("range = %#x", c.rng)
	}
}

func TestContextStateRange(t *testing.T) {
	var c cabac

	for _, st := range []sliceType{sliceI, sliceP, sliceB} {
		for qp := int32(0); qp <= 51; qp++ {
			c.initContexts(qp, st, false)

			for i, s := range c.state {
				if s > 125 {
					t.Fatalf("slice %d qp %d ctx %d state %d out of range", st, qp, i, s)
				}
			}
		}
	}
}
