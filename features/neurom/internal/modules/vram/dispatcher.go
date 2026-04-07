package vram

import (
	"math/rand"
	"strconv"
	"sync"
	"time"
)

type dispatchStrategy interface {
	Decide(command string, dataSize int) int
	Feedback(command string, dataSize int, workers int, elapsed time.Duration)
}

func newDispatchStrategy(strategy string, maxWorkers int) dispatchStrategy {
	switch strategy {
	case "static":
		return &staticDispatcher{maxWorkers: maxWorkers}
	case "heuristic", "dynamic":
		return newDynamicDispatcher(maxWorkers)
	default:
		return newAdaptiveDispatcher(maxWorkers)
	}
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

// --- adaptive (Contextual Multi-Armed Bandit) ---

const (
	emaAlpha                   = 0.15
	defaultEpsilon             = 0.10
	warmupTrials               = 5
	adaptiveFeedbackSampleRate = 1
)

type armStats struct {
	emaReward float64
	trials    int
}

type bandit struct {
	arms      map[int]*armStats
	warmupIdx int
}

func (b *bandit) getArm(workers int) *armStats {
	a, ok := b.arms[workers]
	if !ok {
		a = &armStats{}
		b.arms[workers] = a
	}
	return a
}

type adaptiveDispatcher struct {
	maxWorkers int
	workerSet  []int
	bandits    map[string]*bandit
	epsilon    float64
	callCount  uint64
	rng        *rand.Rand
	mu         sync.Mutex
}

func buildWorkerSet(maxWorkers int) []int {
	set := []int{1}
	w := 2
	for w < maxWorkers {
		set = append(set, w)
		w *= 2
	}
	if maxWorkers > 1 && set[len(set)-1] != maxWorkers {
		set = append(set, maxWorkers)
	}
	return set
}

func sizeBucket(dataSize int) int {
	switch {
	case dataSize < 256:
		return 0
	case dataSize < 1024:
		return 1
	case dataSize < 4096:
		return 2
	case dataSize < 16384:
		return 3
	case dataSize < 65536:
		return 4
	default:
		return 5
	}
}

func newAdaptiveDispatcher(maxWorkers int) *adaptiveDispatcher {
	return &adaptiveDispatcher{
		maxWorkers: maxWorkers,
		workerSet:  buildWorkerSet(maxWorkers),
		bandits:    make(map[string]*bandit),
		epsilon:    defaultEpsilon,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (d *adaptiveDispatcher) getBandit(key string) *bandit {
	b, ok := d.bandits[key]
	if !ok {
		b = &bandit{arms: make(map[int]*armStats)}
		d.bandits[key] = b
	}
	return b
}

func (d *adaptiveDispatcher) needsWarmup(b *bandit) bool {
	for _, w := range d.workerSet {
		if b.getArm(w).trials < warmupTrials {
			return true
		}
	}
	return false
}

func (d *adaptiveDispatcher) warmupArm(b *bandit) int {
	w := d.workerSet[b.warmupIdx%len(d.workerSet)]
	b.warmupIdx++
	return w
}

func (d *adaptiveDispatcher) bestArm(b *bandit) int {
	best := d.workerSet[0]
	bestReward := -1.0
	for _, w := range d.workerSet {
		r := b.getArm(w).emaReward
		if r > bestReward {
			bestReward = r
			best = w
		}
	}
	return best
}

func (d *adaptiveDispatcher) randomArm() int {
	return d.workerSet[d.rng.Intn(len(d.workerSet))]
}

func (d *adaptiveDispatcher) Decide(command string, dataSize int) int {
	d.mu.Lock()
	defer d.mu.Unlock()

	bucket := sizeBucket(dataSize)
	key := command + ":" + strconv.Itoa(bucket)
	b := d.getBandit(key)

	if d.needsWarmup(b) {
		return d.warmupArm(b)
	}

	if d.rng.Float64() < d.epsilon {
		return d.randomArm()
	}
	return d.bestArm(b)
}

func (d *adaptiveDispatcher) Feedback(command string, dataSize int, workers int, elapsed time.Duration) {
	if dataSize <= 0 || elapsed <= 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.callCount++
	if d.callCount%adaptiveFeedbackSampleRate != 0 {
		return
	}

	bucket := sizeBucket(dataSize)
	key := command + ":" + strconv.Itoa(bucket)
	b := d.getBandit(key)

	reward := float64(dataSize) / float64(elapsed.Nanoseconds())
	arm := b.getArm(workers)
	if arm.trials == 0 {
		arm.emaReward = reward
	} else {
		arm.emaReward = (1-emaAlpha)*arm.emaReward + emaAlpha*reward
	}
	arm.trials++
}

