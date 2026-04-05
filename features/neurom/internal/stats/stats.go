package stats

import (
	"sync"
	"time"
)

const SlotCount = 30

// WindowStats holds aggregated stats for a single time window.
type WindowStats struct {
	Count int64 `json:"count"`
	AvgNs int64 `json:"avg_ns"`
	MaxNs int64 `json:"max_ns"`
}

// CommandStat holds stats for a command across multiple time windows.
type CommandStat struct {
	Last1s  WindowStats `json:"last_1s"`
	Last10s WindowStats `json:"last_10s"`
	Last30s WindowStats `json:"last_30s"`
}

// MonitorStats holds monitor performance statistics for multi-window display.
type MonitorStats struct {
	FPS1s  float64 `json:"fps_1s"`
	FPS10s float64 `json:"fps_10s"`
	FPS30s float64 `json:"fps_30s"`
}

type windowAccum struct {
	count   int64
	totalNs int64
	maxNs   int64
}

type slotData struct {
	second int64
	accum  map[string]*windowAccum
}

// Collector accumulates per-command execution timing within 1-second slots
// stored in a ring buffer of SlotCount entries.
// Snapshot() is side-effect-free and returns stats for 1s, 10s, and 30s windows.
type Collector struct {
	mu        sync.Mutex
	slots     [SlotCount]slotData
	headIdx   int
	knownCmds map[string]struct{}
	nowFunc   func() time.Time
}

// NewCollector creates a Collector with an empty initial state.
func NewCollector() *Collector {
	now := time.Now()
	c := &Collector{
		knownCmds: make(map[string]struct{}),
		nowFunc:   time.Now,
	}
	c.slots[0] = slotData{
		second: now.Truncate(time.Second).Unix(),
		accum:  make(map[string]*windowAccum),
	}
	return c
}

// SetNowFunc overrides the time source (for testing).
func (c *Collector) SetNowFunc(fn func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nowFunc = fn
	sec := fn().Truncate(time.Second).Unix()
	c.slots[0] = slotData{second: sec, accum: make(map[string]*windowAccum)}
	c.headIdx = 0
}

// Record records a single command execution with the given duration.
// Window rotation happens here when the wall-clock second changes.
func (c *Collector) Record(command string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sec := c.nowFunc().Truncate(time.Second).Unix()
	if sec > c.slots[c.headIdx].second {
		c.advance(sec)
	}

	c.knownCmds[command] = struct{}{}

	acc := c.slots[c.headIdx].accum[command]
	if acc == nil {
		acc = &windowAccum{}
		c.slots[c.headIdx].accum[command] = acc
	}
	acc.count++
	ns := d.Nanoseconds()
	acc.totalNs += ns
	if ns > acc.maxNs {
		acc.maxNs = ns
	}
}

// Snapshot returns stats for all known commands across 1s, 10s, and 30s windows.
// This method is side-effect-free: it never rotates the ring buffer.
func (c *Collector) Snapshot() map[string]CommandStat {
	c.mu.Lock()
	defer c.mu.Unlock()

	currentSec := c.nowFunc().Truncate(time.Second).Unix()
	result := make(map[string]CommandStat, len(c.knownCmds))

	for cmd := range c.knownCmds {
		var s1, s10, s30 aggregator

		for i := range SlotCount {
			slot := &c.slots[i]
			if slot.accum == nil {
				continue
			}
			age := currentSec - slot.second
			if age < 1 || age > 30 {
				continue
			}
			acc := slot.accum[cmd]
			if acc == nil {
				continue
			}
			if age == 1 {
				s1.add(acc)
			}
			if age <= 10 {
				s10.add(acc)
			}
			s30.add(acc)
		}

		result[cmd] = CommandStat{
			Last1s:  s1.toWindowStats(),
			Last10s: s10.toWindowStats(),
			Last30s: s30.toWindowStats(),
		}
	}
	return result
}

func (c *Collector) advance(sec int64) {
	oldSec := c.slots[c.headIdx].second
	gap := sec - oldSec
	steps := int(gap)
	if steps > SlotCount {
		steps = SlotCount
	}
	for i := 1; i <= steps; i++ {
		idx := (c.headIdx + i) % SlotCount
		c.slots[idx] = slotData{
			second: sec - int64(steps) + int64(i),
			accum:  make(map[string]*windowAccum),
		}
	}
	c.headIdx = (c.headIdx + steps) % SlotCount
}

type aggregator struct {
	count   int64
	totalNs int64
	maxNs   int64
}

func (a *aggregator) add(acc *windowAccum) {
	a.count += acc.count
	a.totalNs += acc.totalNs
	if acc.maxNs > a.maxNs {
		a.maxNs = acc.maxNs
	}
}

func (a *aggregator) toWindowStats() WindowStats {
	avg := int64(0)
	if a.count > 0 {
		avg = a.totalNs / a.count
	}
	return WindowStats{Count: a.count, AvgNs: avg, MaxNs: a.maxNs}
}
