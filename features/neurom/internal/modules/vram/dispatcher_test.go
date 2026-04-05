package vram

import (
	"testing"
	"time"
)

func TestStaticDispatcher_AlwaysReturnsMax(t *testing.T) {
	d := &staticDispatcher{maxWorkers: 4}

	cases := []struct {
		command  string
		dataSize int
	}{
		{"clear_vram", 1},
		{"blit_rect", 100000},
		{"unknown_cmd", 0},
	}
	for _, tc := range cases {
		got := d.Decide(tc.command, tc.dataSize)
		if got != 4 {
			t.Errorf("Decide(%q, %d) = %d, want 4", tc.command, tc.dataSize, got)
		}
	}
}

func TestStaticDispatcher_FeedbackNoop(t *testing.T) {
	d := &staticDispatcher{maxWorkers: 4}

	d.Feedback("clear_vram", 50000, 4, 100*time.Microsecond)
	d.Feedback("blit_rect", 100, 1, 10*time.Microsecond)

	if got := d.Decide("clear_vram", 1); got != 4 {
		t.Errorf("Decide after Feedback = %d, want 4", got)
	}
}

func TestDynamicDispatcher_BelowThreshold(t *testing.T) {
	d := newDynamicDispatcher(4)

	got := d.Decide("blit_rect", 100)
	if got != 1 {
		t.Errorf("Decide(blit_rect, 100) = %d, want 1 (below threshold 16384)", got)
	}
}

func TestDynamicDispatcher_AboveThreshold(t *testing.T) {
	d := newDynamicDispatcher(4)

	got := d.Decide("blit_rect", 20000)
	if got != 4 {
		t.Errorf("Decide(blit_rect, 20000) = %d, want 4 (above threshold 16384)", got)
	}
}

func TestDynamicDispatcher_UnknownCommand(t *testing.T) {
	d := newDynamicDispatcher(4)

	below := d.Decide("unknown_cmd", 100)
	if below != 1 {
		t.Errorf("Decide(unknown, 100) = %d, want 1 (below defaultThreshold)", below)
	}

	above := d.Decide("unknown_cmd", defaultThreshold+1)
	if above != 4 {
		t.Errorf("Decide(unknown, %d) = %d, want 4 (above defaultThreshold)", defaultThreshold+1, above)
	}
}

func TestDynamicDispatcher_FeedbackIncreasesThreshold(t *testing.T) {
	d := newDynamicDispatcher(4)

	tracker := d.trackers["blit_rect"]
	initialThreshold := tracker.threshold

	singleDur := 200 * time.Microsecond
	d.Feedback("blit_rect", 20000, 1, singleDur)

	parallelTime := time.Duration(float64(singleDur) / 1.2)
	d.Feedback("blit_rect", 20000, 4, parallelTime)

	if tracker.threshold <= initialThreshold {
		t.Errorf("threshold should increase when speedup ratio < 1.5, got %d (was %d)",
			tracker.threshold, initialThreshold)
	}
}

func TestDynamicDispatcher_FeedbackDecreasesThreshold(t *testing.T) {
	d := newDynamicDispatcher(4)

	tracker := d.trackers["clear_vram"]
	initialThreshold := tracker.threshold

	singleDur := 500 * time.Microsecond
	d.Feedback("clear_vram", 50000, 1, singleDur)

	parallelTime := time.Duration(float64(singleDur) / 3.0)
	d.Feedback("clear_vram", 50000, 4, parallelTime)

	if tracker.threshold >= initialThreshold {
		t.Errorf("threshold should decrease when speedup ratio > 2.0, got %d (was %d)",
			tracker.threshold, initialThreshold)
	}
}

func TestDynamicDispatcher_ThresholdBounds(t *testing.T) {
	d := newDynamicDispatcher(4)

	tracker := d.trackers["clear_vram"]
	tracker.threshold = minThreshold + 1
	tracker.singleEMA = 100.0
	tracker.samples = 10

	baseDur := 100 * time.Nanosecond * 50000
	parallelTime := time.Duration(float64(baseDur) / 3.0)
	d.Feedback("clear_vram", 50000, 4, parallelTime)

	if tracker.threshold < minThreshold {
		t.Errorf("threshold %d fell below minThreshold %d", tracker.threshold, minThreshold)
	}

	tracker.threshold = maxThreshold - 1
	tracker.singleEMA = 100.0

	parallelTime = time.Duration(float64(baseDur) / 1.0)
	d.Feedback("clear_vram", 50000, 4, parallelTime)

	if tracker.threshold > maxThreshold {
		t.Errorf("threshold %d exceeded maxThreshold %d", tracker.threshold, maxThreshold)
	}
}

func TestDynamicDispatcher_SingleWorkerNoFeedback(t *testing.T) {
	d := newDynamicDispatcher(4)

	tracker := d.trackers["blit_rect"]
	initialThreshold := tracker.threshold

	for range 10 {
		d.Feedback("blit_rect", 100, 1, 1*time.Microsecond)
	}

	if tracker.threshold != initialThreshold {
		t.Errorf("threshold changed from %d to %d after single-worker feedback",
			initialThreshold, tracker.threshold)
	}
}

func TestNewDispatchStrategy_Static(t *testing.T) {
	s := newDispatchStrategy("static", 4)
	if _, ok := s.(*staticDispatcher); !ok {
		t.Errorf("expected *staticDispatcher, got %T", s)
	}
}

func TestNewDispatchStrategy_Dynamic(t *testing.T) {
	s := newDispatchStrategy("dynamic", 4)
	if _, ok := s.(*dynamicDispatcher); !ok {
		t.Errorf("expected *dynamicDispatcher, got %T", s)
	}
}

func TestNewDispatchStrategy_Default(t *testing.T) {
	s := newDispatchStrategy("", 4)
	if _, ok := s.(*dynamicDispatcher); !ok {
		t.Errorf("expected *dynamicDispatcher for empty string, got %T", s)
	}
}

func TestFeedbackSampling_FirstCallProcessed(t *testing.T) {
	d := newDynamicDispatcher(4)
	tracker := d.trackers["blit_rect"]

	if tracker.samples != 0 {
		t.Fatalf("initial samples = %d, want 0", tracker.samples)
	}

	d.Feedback("blit_rect", 20000, 1, 200*time.Microsecond)

	if tracker.samples != 1 {
		t.Errorf("samples = %d, want 1 after first call", tracker.samples)
	}
	if tracker.singleEMA <= 0 {
		t.Errorf("singleEMA = %f, want > 0 after bootstrap", tracker.singleEMA)
	}
}

func TestFeedbackSampling_SkipsMostCalls(t *testing.T) {
	d := newDynamicDispatcher(4)
	tracker := d.trackers["blit_rect"]

	d.Feedback("blit_rect", 20000, 1, 200*time.Microsecond)
	initialEMA := tracker.singleEMA

	for range feedbackSampleRate - 2 {
		d.Feedback("blit_rect", 20000, 1, 500*time.Microsecond)
	}

	if tracker.singleEMA != initialEMA {
		t.Errorf("EMA changed from %f to %f during skipped calls", initialEMA, tracker.singleEMA)
	}

	d.Feedback("blit_rect", 20000, 1, 500*time.Microsecond)

	if tracker.singleEMA == initialEMA {
		t.Errorf("EMA should have changed at sample rate boundary, still %f", tracker.singleEMA)
	}
}

func TestFeedbackSampling_ParallelAlwaysProcessed(t *testing.T) {
	d := newDynamicDispatcher(4)
	tracker := d.trackers["clear_vram"]

	singleDur := 500 * time.Microsecond
	d.Feedback("clear_vram", 50000, 1, singleDur)

	initialThreshold := tracker.threshold

	parallelTime := time.Duration(float64(singleDur) / 3.0)
	d.Feedback("clear_vram", 50000, 4, parallelTime)

	if tracker.threshold >= initialThreshold {
		t.Errorf("threshold should decrease for parallel, got %d (was %d)",
			tracker.threshold, initialThreshold)
	}
}
