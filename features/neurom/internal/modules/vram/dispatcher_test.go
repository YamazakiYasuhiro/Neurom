package vram

import (
	"reflect"
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

func TestNewDispatchStrategy_Heuristic(t *testing.T) {
	s := newDispatchStrategy("heuristic", 4)
	if _, ok := s.(*dynamicDispatcher); !ok {
		t.Errorf("expected *dynamicDispatcher for 'heuristic', got %T", s)
	}
}

func TestNewDispatchStrategy_Adaptive(t *testing.T) {
	s := newDispatchStrategy("adaptive", 4)
	if _, ok := s.(*adaptiveDispatcher); !ok {
		t.Errorf("expected *adaptiveDispatcher, got %T", s)
	}
}

func TestNewDispatchStrategy_Default(t *testing.T) {
	s := newDispatchStrategy("", 4)
	if _, ok := s.(*adaptiveDispatcher); !ok {
		t.Errorf("expected *adaptiveDispatcher for empty string, got %T", s)
	}
}

func TestSizeBucket(t *testing.T) {
	cases := []struct {
		dataSize int
		want     int
	}{
		{0, 0}, {255, 0},
		{256, 1}, {1023, 1},
		{1024, 2}, {4095, 2},
		{4096, 3}, {16383, 3},
		{16384, 4}, {65535, 4},
		{65536, 5}, {1000000, 5},
	}
	for _, tc := range cases {
		got := sizeBucket(tc.dataSize)
		if got != tc.want {
			t.Errorf("sizeBucket(%d) = %d, want %d", tc.dataSize, got, tc.want)
		}
	}
}

func TestBuildWorkerSet(t *testing.T) {
	cases := []struct {
		maxWorkers int
		want       []int
	}{
		{1, []int{1}},
		{2, []int{1, 2}},
		{4, []int{1, 2, 4}},
		{8, []int{1, 2, 4, 8}},
		{6, []int{1, 2, 4, 6}},
		{16, []int{1, 2, 4, 8, 16}},
	}
	for _, tc := range cases {
		got := buildWorkerSet(tc.maxWorkers)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("buildWorkerSet(%d) = %v, want %v", tc.maxWorkers, got, tc.want)
		}
	}
}

func TestAdaptiveDispatcher_DecideReturnsValidWorker(t *testing.T) {
	d := newAdaptiveDispatcher(8)
	for range 100 {
		w := d.Decide("blit_rect", 5000)
		valid := false
		for _, ws := range d.workerSet {
			if w == ws {
				valid = true
				break
			}
		}
		if !valid {
			t.Errorf("Decide returned %d, not in workerSet %v", w, d.workerSet)
		}
	}
}

func TestAdaptiveDispatcher_ExploitBestArm(t *testing.T) {
	d := newAdaptiveDispatcher(8)
	d.epsilon = 0.0

	key := "clear_vram:5"
	b := d.getBandit(key)
	for _, w := range d.workerSet {
		arm := b.getArm(w)
		arm.trials = 10
		arm.emaReward = 1.0
	}
	b.getArm(4).emaReward = 10.0

	for range 50 {
		w := d.Decide("clear_vram", 100000)
		if w != 4 {
			t.Errorf("ε=0 should always select best arm (4), got %d", w)
		}
	}
}

func TestAdaptiveDispatcher_ExploreRandomArm(t *testing.T) {
	d := newAdaptiveDispatcher(8)
	d.epsilon = 1.0

	key := "blit_rect:3"
	b := d.getBandit(key)
	for _, w := range d.workerSet {
		arm := b.getArm(w)
		arm.trials = 10
		arm.emaReward = 1.0
	}

	counts := make(map[int]int)
	n := 1000
	for range n {
		w := d.Decide("blit_rect", 5000)
		counts[w]++
	}
	for _, w := range d.workerSet {
		if counts[w] == 0 {
			t.Errorf("ε=1.0: arm %d was never selected in %d trials", w, n)
		}
	}
}

func TestAdaptiveDispatcher_Rollback(t *testing.T) {
	d := newAdaptiveDispatcher(4)
	d.epsilon = 0.0

	key := "clear_vram:5"
	b := d.getBandit(key)
	for _, w := range d.workerSet {
		arm := b.getArm(w)
		arm.trials = 10
		arm.emaReward = 1.0
	}
	b.getArm(4).emaReward = 10.0

	w := d.Decide("clear_vram", 100000)
	if w != 4 {
		t.Fatalf("initial best should be 4, got %d", w)
	}

	b.getArm(4).emaReward = 0.5
	b.getArm(2).emaReward = 5.0

	w = d.Decide("clear_vram", 100000)
	if w != 2 {
		t.Errorf("after rollback, expected arm 2, got %d", w)
	}
}

func TestAdaptiveDispatcher_EMAUpdate(t *testing.T) {
	d := newAdaptiveDispatcher(4)
	for i := uint64(0); i < adaptiveFeedbackSampleRate; i++ {
		d.Feedback("clear_vram", 50000, 4, 100*time.Microsecond)
	}
	key := "clear_vram:4"
	b := d.bandits[key]
	if b == nil {
		t.Fatal("bandit not created")
	}
	arm := b.arms[4]
	if arm == nil {
		t.Fatal("arm not created")
	}
	if arm.emaReward <= 0 {
		t.Errorf("emaReward should be > 0 after feedback, got %f", arm.emaReward)
	}
	if arm.trials != 1 {
		t.Errorf("trials = %d, want 1", arm.trials)
	}
}

func TestAdaptiveDispatcher_FeedbackSampling(t *testing.T) {
	d := newAdaptiveDispatcher(4)

	// With adaptiveFeedbackSampleRate=1, every Feedback call is processed.
	d.Feedback("blit_rect", 5000, 1, 10*time.Microsecond)
	if len(d.bandits) == 0 {
		t.Error("bandits should have entry after first feedback")
	}

	// Verify additional feedback updates the same bandit correctly.
	d.Feedback("blit_rect", 5000, 1, 10*time.Microsecond)
	key := "blit_rect:3"
	b := d.bandits[key]
	if b == nil {
		t.Fatal("bandit for blit_rect:3 not found")
	}
	arm := b.arms[1]
	if arm == nil {
		t.Fatal("arm for workers=1 not found")
	}
	if arm.trials != 2 {
		t.Errorf("trials = %d, want 2 after two feedback calls", arm.trials)
	}
}

func TestAdaptiveDispatcher_Warmup(t *testing.T) {
	d := newAdaptiveDispatcher(4)
	d.epsilon = 0.0

	seen := make(map[int]bool)
	for range 100 {
		w := d.Decide("clear_vram", 100000)
		seen[w] = true
	}
	for _, w := range d.workerSet {
		if !seen[w] {
			t.Errorf("warmup: arm %d was never selected", w)
		}
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
