package vram

import "sync"

type rowTask struct {
	fn       func(startRow, endRow int)
	startRow int
	endRow   int
	wg       *sync.WaitGroup
}

type workerPool struct {
	tasks chan rowTask
	quit  chan struct{}
	size  int
	wg    sync.WaitGroup
}

func newWorkerPool(size int) *workerPool {
	p := &workerPool{
		tasks: make(chan rowTask, size*2),
		quit:  make(chan struct{}),
		size:  size,
	}
	p.wg.Add(size)
	for range size {
		go p.worker()
	}
	return p
}

func (p *workerPool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.quit:
			return
		case task := <-p.tasks:
			task.fn(task.startRow, task.endRow)
			task.wg.Done()
		}
	}
}

func (p *workerPool) stop() {
	close(p.quit)
	p.wg.Wait()
}

// splitRows divides totalRows into numWorkers contiguous chunks.
// Each chunk is [startRow, endRow). If totalRows < numWorkers,
// only totalRows chunks are returned.
func splitRows(totalRows, numWorkers int) [][2]int {
	if totalRows <= 0 {
		return nil
	}
	if numWorkers > totalRows {
		numWorkers = totalRows
	}
	base := totalRows / numWorkers
	remainder := totalRows % numWorkers
	chunks := make([][2]int, numWorkers)
	start := 0
	for i := range numWorkers {
		rows := base
		if i < remainder {
			rows++
		}
		chunks[i] = [2]int{start, start + rows}
		start += rows
	}
	return chunks
}
