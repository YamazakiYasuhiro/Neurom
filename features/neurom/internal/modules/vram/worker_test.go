package vram

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSplitRows(t *testing.T) {
	cases := []struct {
		name       string
		totalRows  int
		numWorkers int
		wantLen    int
	}{
		{"212 rows / 4 workers", 212, 4, 4},
		{"212 rows / 1 worker", 212, 1, 1},
		{"3 rows / 8 workers", 3, 8, 3},
		{"1 row / 4 workers", 1, 4, 1},
		{"0 rows / 4 workers", 0, 4, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := splitRows(tc.totalRows, tc.numWorkers)
			if len(chunks) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(chunks), tc.wantLen)
			}
			if len(chunks) == 0 {
				return
			}
			if chunks[0][0] != 0 {
				t.Errorf("first chunk start = %d, want 0", chunks[0][0])
			}
			if chunks[len(chunks)-1][1] != tc.totalRows {
				t.Errorf("last chunk end = %d, want %d", chunks[len(chunks)-1][1], tc.totalRows)
			}
			for i := 1; i < len(chunks); i++ {
				if chunks[i][0] != chunks[i-1][1] {
					t.Errorf("gap between chunk %d and %d: %d != %d", i-1, i, chunks[i-1][1], chunks[i][0])
				}
			}
		})
	}
}

func TestWorkerPoolStartStop(t *testing.T) {
	before := runtime.NumGoroutine()

	pool := newWorkerPool(4)

	var wg sync.WaitGroup
	wg.Add(1)
	pool.tasks <- rowTask{
		fn:       func(s, e int) {},
		startRow: 0,
		endRow:   1,
		wg:       &wg,
	}
	wg.Wait()

	pool.stop()

	runtime.Gosched()
	after := runtime.NumGoroutine()
	if after > before+1 {
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}

func TestWorkerPoolExecution(t *testing.T) {
	pool := newWorkerPool(4)
	defer pool.stop()

	totalRows := 100
	var counter atomic.Int64
	visited := make([]bool, totalRows)
	var mu sync.Mutex

	chunks := splitRows(totalRows, 4)
	var wg sync.WaitGroup
	wg.Add(len(chunks))
	for _, c := range chunks {
		pool.tasks <- rowTask{
			fn: func(s, e int) {
				for row := s; row < e; row++ {
					counter.Add(1)
					mu.Lock()
					visited[row] = true
					mu.Unlock()
				}
			},
			startRow: c[0],
			endRow:   c[1],
			wg:       &wg,
		}
	}
	wg.Wait()

	if counter.Load() != int64(totalRows) {
		t.Errorf("processed %d rows, want %d", counter.Load(), totalRows)
	}
	for i, v := range visited {
		if !v {
			t.Errorf("row %d was not visited", i)
		}
	}
}
