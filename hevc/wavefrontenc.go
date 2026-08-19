package hevc

import "sync"

// encodeWavefront codes the coding tree block rows of 7.3.8.1 at once. Each row
// runs on its own copy of the encoder: the planes and the per block tables are
// slices and stay shared, the scratch and the writers are values and come out
// per row.
func (e *intraEncoder[P]) encodeWavefront(rows, cols, workers int) ([][]byte, error) {
	v := newWave(rows, 0)
	subs := make([][]byte, rows)

	var wg sync.WaitGroup

	for id := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			r := *e
			r.cabac.bits = &r.bits

			for k := id; k < rows; k += workers {
				if v.failed() {
					return
				}

				sub, err := r.waveRow(v, k, rows, cols)
				if err != nil {
					v.fail(err)

					return
				}

				subs[k] = sub
			}
		}()
	}

	wg.Wait()

	if v.err != nil {
		return nil, v.err
	}

	return subs, nil
}

// waveRow codes one row at a two block lag behind the row above, which carries
// 9.3.1's contexts and leaves the samples 8.4.4.2.2 reads reconstructed.
func (e *intraEncoder[P]) waveRow(v *wave, k, rows, cols int) ([]byte, error) {
	e.bits = putBits{}
	e.cabac.init(&e.bits, int32(e.qp), sliceI, false)

	if k > 0 {
		if !v.await(k-1, min(2, cols)) {
			return nil, nil
		}

		e.cabac.state = v.ctx[k-1]
	}

	for x := range cols {
		if k > 0 && !v.await(k-1, min(x+2, cols)) {
			return nil, nil
		}

		if err := e.tree(x*64, k*64, 6, 0); err != nil {
			return nil, err
		}

		last := k == rows-1 && x == cols-1
		e.cabac.encodeTerminate(boolToBit(last))

		if x == min(1, cols-1) {
			v.saveCtx(k, e.cabac.state, x+1)
		} else {
			v.advance(k, x+1)
		}

		if !last && x == cols-1 {
			e.cabac.encodeTerminate(1)
		}
	}

	return e.cabac.bytes(), nil
}
