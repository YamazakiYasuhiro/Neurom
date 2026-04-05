package vram

import (
	"sync"
	"time"
)

type dispatchStrategy interface {
	Decide(command string, dataSize int) int
	Feedback(command string, dataSize int, workers int, elapsed time.Duration)
}

func newDispatchStrategy(strategy string, maxWorkers int) dispatchStrategy {
	if strategy == "static" {
		return &staticDispatcher{maxWorkers: maxWorkers}
	}
	return newDynamicDispatcher(maxWorkers)
}

// --- static ---

type staticDispatcher struct {
	maxWorkers int
}

func (s *staticDispatcher) Decide(_ string, _ int) int {
	return s.maxWorkers
}

func (s *staticDispatcher) Feedback(_ string, _ int, _ int, _ time.Duration) {}

// --- dynamic ---

const (
	minThreshold     = 64
	maxThreshold     = 262144
	defaultThreshold = 16384
	feedbackAlpha    = 0.1
	speedupLow       = 1.5
	speedupHigh      = 2.0
	thresholdStep      = 0.2
	feedbackSampleRate = 64
)

type cmdTracker struct {
	threshold int
	singleEMA float64
	samples   int
	callCount uint64
}

type dynamicDispatcher struct {
	maxWorkers int
	trackers   map[string]*cmdTracker
	mu         sync.Mutex
}

func newDynamicDispatcher(maxWorkers int) *dynamicDispatcher {
	return &dynamicDispatcher{
		maxWorkers: maxWorkers,
		trackers: map[string]*cmdTracker{
			"clear_vram":          {threshold: 4096},
			"blit_rect":           {threshold: 16384},
			"blit_rect_transform": {threshold: 32768},
			"copy_rect":           {threshold: 8192},
		},
	}
}

func (d *dynamicDispatcher) Decide(command string, dataSize int) int {
	d.mu.Lock()
	defer d.mu.Unlock()

	tracker := d.getTracker(command)
	if dataSize < tracker.threshold {
		return 1
	}
	return d.maxWorkers
}

func (d *dynamicDispatcher) Feedback(command string, dataSize int, workers int, elapsed time.Duration) {
	if dataSize <= 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	tracker := d.getTracker(command)
	nsPerPixel := float64(elapsed.Nanoseconds()) / float64(dataSize)

	if workers == 1 {
		tracker.callCount++
		if tracker.samples > 0 && tracker.callCount%feedbackSampleRate != 0 {
			return
		}
		if tracker.samples == 0 {
			tracker.singleEMA = nsPerPixel
		} else {
			tracker.singleEMA = (1-feedbackAlpha)*tracker.singleEMA + feedbackAlpha*nsPerPixel
		}
		tracker.samples++
		return
	}

	if tracker.samples == 0 {
		return
	}

	ratio := tracker.singleEMA / nsPerPixel
	if ratio < speedupLow {
		newTh := int(float64(tracker.threshold) * (1 + thresholdStep))
		if newTh > maxThreshold {
			newTh = maxThreshold
		}
		tracker.threshold = newTh
	} else if ratio > speedupHigh {
		newTh := int(float64(tracker.threshold) * (1 - thresholdStep))
		if newTh < minThreshold {
			newTh = minThreshold
		}
		tracker.threshold = newTh
	}
}

func (d *dynamicDispatcher) getTracker(command string) *cmdTracker {
	t, ok := d.trackers[command]
	if !ok {
		t = &cmdTracker{threshold: defaultThreshold}
		d.trackers[command] = t
	}
	return t
}
