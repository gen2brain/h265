package hevc

import "testing"

func TestGetBits(t *testing.T) {
	var c getBits
	c.init([]byte{0b1010_0011, 0b1100_0000})

	if got := c.bits(4); got != 0b1010 {
		t.Fatalf("bits(4) = %#b", got)
	}

	if got := c.bits(4); got != 0b0011 {
		t.Fatalf("bits(4) = %#b", got)
	}

	if got := c.bits(2); got != 0b11 {
		t.Fatalf("bits(2) = %#b", got)
	}
}

func TestGetBitsUE(t *testing.T) {
	var c getBits
	c.init([]byte{0xa6, 0x40})

	for i, want := range []uint32{0, 1, 2, 3} {
		if got := c.ue(); got != want {
			t.Fatalf("ue %d = %d, want %d", i, got, want)
		}
	}
}

func TestGetBitsSE(t *testing.T) {
	var c getBits
	c.init([]byte{0xa6, 0x42, 0x80})

	for i, want := range []int32{0, 1, -1, 2, -2} {
		if got := c.se(); got != want {
			t.Fatalf("se %d = %d, want %d", i, got, want)
		}
	}
}

func TestPutBitsRoundTrip(t *testing.T) {
	var w putBits
	w.bits(0b101, 3)
	w.ue(0)
	w.ue(1)
	w.ue(17)
	w.se(-4)
	w.se(5)
	w.rbspTrailingBits()

	var c getBits
	c.init(w.bytes())

	if got := c.bits(3); got != 0b101 {
		t.Fatalf("bits = %#b", got)
	}

	for _, want := range []uint32{0, 1, 17} {
		if got := c.ue(); got != want {
			t.Fatalf("ue = %d, want %d", got, want)
		}
	}

	for _, want := range []int32{-4, 5} {
		if got := c.se(); got != want {
			t.Fatalf("se = %d, want %d", got, want)
		}
	}

	if c.bit() != 1 || c.moreRBSPData() {
		t.Fatalf("trailing bits = %#x", w.bytes())
	}
}

func TestGetBitsOverread(t *testing.T) {
	var c getBits
	c.init([]byte{0xff})

	if got := c.bits(8); got != 0xff {
		t.Fatalf("bits(8) = %#x", got)
	}

	if got := c.bits(8); got != 0 {
		t.Fatalf("bits(8) past end = %#x", got)
	}

	if !c.err {
		t.Fatal("err not set after overread")
	}
}

func TestGetBitsSkip(t *testing.T) {
	var c getBits
	c.init([]byte{0xde, 0xad, 0xbe, 0xef})

	c.skip(16)

	if got := c.bits(16); got != 0xbeef {
		t.Fatalf("bits(16) = %#x", got)
	}
}

func TestGetBitsAlign(t *testing.T) {
	var c getBits
	c.init([]byte{0xff, 0x0f})

	c.bits(3)
	c.byteAlign()

	if got := c.pos(); got != 8 {
		t.Fatalf("pos = %d", got)
	}

	if got := c.bits(8); got != 0x0f {
		t.Fatalf("bits(8) = %#x", got)
	}

	c.byteAlign()

	if got := c.pos(); got != 16 {
		t.Fatalf("pos after aligned align = %d", got)
	}
}

func TestGetBitsMoreRBSPData(t *testing.T) {
	var c getBits
	c.init([]byte{0xa0})

	if !c.moreRBSPData() {
		t.Fatal("want data before the stop bit")
	}

	c.bits(2)

	if c.moreRBSPData() {
		t.Fatal("want no data at the stop bit")
	}

	c.init([]byte{0x80, 0x00, 0x00})

	if c.moreRBSPData() {
		t.Fatal("want no data with trailing zero bytes")
	}

	c.init([]byte{0x12, 0x80})

	if !c.moreRBSPData() {
		t.Fatal("want data while before the last non-zero byte")
	}

	c.bits(8)

	if c.moreRBSPData() {
		t.Fatal("want no data at a lone stop bit")
	}
}
