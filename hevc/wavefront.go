package hevc

import (
	"runtime"
	"sync"
)

// Threads bounds the goroutines decoding one picture's wavefront rows. Zero
// means GOMAXPROCS, one decodes serially. It is read once per slice segment,
// so changing it mid-stream takes effect at the next one.
func (d *Decoder) Threads(n int) { d.threads = n }

func (d *Decoder) waveThreads() int {
	if d.threads == 0 {
		return runtime.GOMAXPROCS(0)
	}

	return max(d.threads, 1)
}

// waveWorkers reports how many goroutines 7.3.8.1 may be spread over. The
// wavefront path takes whole rows of one tile, so anything else — tiles, a
// segment starting mid-row, a single row — stays on the serial loop.
func (d *ctuDecoder) waveWorkers(sh *sliceHeader, starts []int, wpp bool) int {
	w := int(d.s.picWidthInCtbs)

	if !wpp || d.p.tilesEnabled || len(starts) < 2 {
		return 1
	}

	// A dependent segment carries the contexts the previous one ended with,
	// and persistent Rice adaptation carries a running state; neither is
	// exercised by anything here, so both stay on the serial loop.
	if d.p.dependentSliceSegmentsEnabled || d.s.persistentRiceAdaptation {
		return 1
	}

	if int(sh.sliceSegmentAddress)%w != 0 {
		return 1
	}

	if int(sh.sliceSegmentAddress)/w+len(starts) > int(d.s.picHeightInCtbs) {
		return 1
	}

	return min(d.threads, len(starts))
}

// wave is the row progress a wavefront synchronises on. done[k] counts the
// coding tree blocks of row k that are reconstructed, and ctx[k] is the
// context state 9.3.1 hands to the row below, valid once done[k] reaches two.
type wave struct {
	mu   sync.Mutex
	cond *sync.Cond
	done []int
	ctx  [][nContexts]uint8
	err  error
	bad  bool
}

func newWave(rows, first int) *wave {
	v := &wave{
		done: make([]int, rows),
		ctx:  make([][nContexts]uint8, rows),
	}

	v.cond = sync.NewCond(&v.mu)
	v.done[0] = first

	return v
}

// await blocks until row k has reconstructed n blocks, reporting false when
// another row has already failed and there is nothing left to wait for.
func (v *wave) await(k, n int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	for v.done[k] < n && !v.bad {
		v.cond.Wait()
	}

	return !v.bad
}

func (v *wave) advance(k, n int) {
	v.mu.Lock()
	v.done[k] = n
	v.mu.Unlock()

	v.cond.Broadcast()
}

// saveCtx publishes the state of 9.3.1 together with the progress that makes
// it readable, so a waiting row never sees one without the other.
func (v *wave) saveCtx(k int, state [nContexts]uint8, n int) {
	v.mu.Lock()
	v.ctx[k] = state
	v.done[k] = n
	v.mu.Unlock()

	v.cond.Broadcast()
}

func (v *wave) fail(err error) {
	v.mu.Lock()

	if v.err == nil {
		v.err = err
	}

	v.bad = true
	v.mu.Unlock()

	v.cond.Broadcast()
}

func (v *wave) failed() bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	return v.bad
}

// decodeWavefront is 7.3.8.1 with the rows of the segment decoded at once.
// Each row runs on its own copy of the block decoder: the tables indexed by
// position are slices and stay shared, while the scratch and the arithmetic
// decoder are values and come out per row.
func (d *ctuDecoder) decodeWavefront(nal NALUnit, sh *sliceHeader, starts []int,
	cur int32, workers int,
) error {
	w := int(d.s.picWidthInCtbs)
	rows := len(starts)
	first := int(sh.sliceSegmentAddress) / w

	v := newWave(rows, 0)

	var wg sync.WaitGroup

	for id := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			r := *d

			for k := id; k < rows; k += workers {
				if v.failed() {
					return
				}

				if err := r.decodeWaveRow(nal, sh, starts, v, k, first+k, cur); err != nil {
					v.fail(err)

					return
				}
			}
		}()
	}

	wg.Wait()

	if v.err != nil {
		return v.err
	}

	if rows > 1 {
		d.saved = v.ctx[rows-1]
		d.hasSaved = true
	}

	return nil
}

// decodeWaveRow decodes one coding tree block row of the segment.
func (d *ctuDecoder) decodeWaveRow(nal NALUnit, sh *sliceHeader, starts []int, v *wave,
	k, row int, cur int32,
) error {
	w := int(d.s.picWidthInCtbs)

	if err := d.startSubstream(nal, sh, starts, k, k == 0); err != nil {
		return err
	}

	if k > 0 {
		// The row above has to reach its second block before this one can
		// start, which is both the context handoff and the samples 6.4.1 needs.
		if !v.await(k-1, 2) {
			return nil
		}

		top := row*w - w + 1
		if top >= d.sliceAddrRs {
			d.c.state = v.ctx[k-1]
		} else {
			d.c.initContexts(sh.qpY, sh.sliceType, sh.cabacInit)
		}
	}

	d.qpYPrev = sh.qpY
	d.qpYCur = sh.qpY

	for col := range w {
		if k > 0 && !v.await(k-1, min(col+2, w)) {
			return nil
		}

		rs := row*w + col

		d.ctbSliceAddr[rs] = int32(d.sliceAddrRs)
		d.ctbSlice[rs] = cur

		if err := d.codingTreeUnit(col<<d.s.ctbLog2SizeY, row<<d.s.ctbLog2SizeY); err != nil {
			return err
		}

		if col == 1 {
			v.saveCtx(k, d.c.state, 2)
		} else {
			v.advance(k, col+1)
		}

		end := d.c.decodeTerminate() != 0

		if end {
			if k != len(starts)-1 {
				return ErrInvalid
			}

			v.advance(k, w)

			return nil
		}

		if col == w-1 {
			if d.c.decodeTerminate() == 0 {
				return ErrInvalid
			}

			v.advance(k, w)
		}
	}

	return nil
}
