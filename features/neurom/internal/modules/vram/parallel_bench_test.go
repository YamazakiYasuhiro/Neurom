package vram

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// BenchmarkParallelInfra measures the overhead of the worker pool dispatch
// mechanism vs direct execution, isolating the infrastructure cost.
func BenchmarkParallelInfra(b *testing.B) {
	const pageW = 256
	const pageH = 212
	totalPixels := pageW * pageH // 54,272

	// Prepare a page buffer to write into (same as clear_vram)
	index := make([]uint8, totalPixels)
	color := make([]uint8, totalPixels*4)
	pal := [4]uint8{0, 0, 0, 255}

	clearFn := func(startRow, endRow int) {
		for y := startRow; y < endRow; y++ {
			for x := 0; x < pageW; x++ {
				i := y*pageW + x
				index[i] = 0
				color[i*4] = pal[0]
				color[i*4+1] = pal[1]
				color[i*4+2] = pal[2]
				color[i*4+3] = pal[3]
			}
		}
	}

	b.Run("direct_single", func(b *testing.B) {
		for b.Loop() {
			clearFn(0, pageH)
		}
	})

	for _, workers := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("direct_goroutine_%d", workers), func(b *testing.B) {
			for b.Loop() {
				chunks := splitRows(pageH, workers)
				var wg sync.WaitGroup
				wg.Add(len(chunks))
				for _, c := range chunks {
					go func() {
						clearFn(c[0], c[1])
						wg.Done()
					}()
				}
				wg.Wait()
			}
		})
	}

	for _, workers := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("pool_%d", workers), func(b *testing.B) {
			pool := newWorkerPool(workers)
			b.ResetTimer()
			for b.Loop() {
				chunks := splitRows(pageH, workers)
				var wg sync.WaitGroup
				wg.Add(len(chunks))
				for _, c := range chunks {
					pool.tasks <- rowTask{fn: clearFn, startRow: c[0], endRow: c[1], wg: &wg}
				}
				wg.Wait()
			}
			b.StopTimer()
			pool.stop()
		})
	}
}

// BenchmarkChannelOverhead measures raw channel send+receive cost
func BenchmarkChannelOverhead(b *testing.B) {
	for _, bufSize := range []int{0, 8, 16} {
		b.Run(fmt.Sprintf("buf_%d", bufSize), func(b *testing.B) {
			ch := make(chan struct{}, bufSize)
			go func() {
				for range ch {
				}
			}()
			b.ResetTimer()
			for b.Loop() {
				ch <- struct{}{}
			}
			b.StopTimer()
			close(ch)
		})
	}
}

// ManualTimingTest prints human-readable timing for quick comparison
func TestParallelTimingManual(t *testing.T) {
	const pageW = 256
	const pageH = 212
	totalPixels := pageW * pageH

	index := make([]uint8, totalPixels)
	color := make([]uint8, totalPixels*4)
	pal := [4]uint8{0, 0, 0, 255}

	clearFn := func(startRow, endRow int) {
		for y := startRow; y < endRow; y++ {
			for x := 0; x < pageW; x++ {
				i := y*pageW + x
				index[i] = 0
				color[i*4] = pal[0]
				color[i*4+1] = pal[1]
				color[i*4+2] = pal[2]
				color[i*4+3] = pal[3]
			}
		}
	}

	const iterations = 1000

	// Single-threaded
	start := time.Now()
	for range iterations {
		clearFn(0, pageH)
	}
	singleDur := time.Since(start)
	singleAvg := singleDur / iterations

	t.Logf("Single-thread:  total=%v  avg=%v", singleDur, singleAvg)

	// Pool-based (mirroring exactly what parallelRows does)
	for _, workers := range []int{2, 4, 8} {
		pool := newWorkerPool(workers)

		start = time.Now()
		for range iterations {
			chunks := splitRows(pageH, workers)
			var wg sync.WaitGroup
			wg.Add(len(chunks))
			for _, c := range chunks {
				pool.tasks <- rowTask{fn: clearFn, startRow: c[0], endRow: c[1], wg: &wg}
			}
			wg.Wait()
		}
		poolDur := time.Since(start)
		poolAvg := poolDur / iterations

		pool.stop()

		speedup := float64(singleDur) / float64(poolDur)
		t.Logf("Pool workers=%d: total=%v  avg=%v  speedup=%.2fx", workers, poolDur, poolAvg, speedup)
	}

	// Direct goroutines (no pool, spawn each time)
	for _, workers := range []int{2, 4, 8} {
		start = time.Now()
		for range iterations {
			chunks := splitRows(pageH, workers)
			var wg sync.WaitGroup
			wg.Add(len(chunks))
			for _, c := range chunks {
				go func() {
					clearFn(c[0], c[1])
					wg.Done()
				}()
			}
			wg.Wait()
		}
		goroutineDur := time.Since(start)
		goroutineAvg := goroutineDur / iterations

		speedup := float64(singleDur) / float64(goroutineDur)
		t.Logf("Goroutine w=%d:  total=%v  avg=%v  speedup=%.2fx", workers, goroutineDur, goroutineAvg, speedup)
	}
}
