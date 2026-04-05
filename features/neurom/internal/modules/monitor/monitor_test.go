package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/stats"
)

type dummyBus struct {
	ch    chan *bus.BusMessage
	pubCh chan *bus.BusMessage
	chans map[string]chan *bus.BusMessage
}

func newDummyBus() *dummyBus {
	return &dummyBus{
		ch:    make(chan *bus.BusMessage, 100),
		pubCh: make(chan *bus.BusMessage, 100),
		chans: make(map[string]chan *bus.BusMessage),
	}
}

func (d *dummyBus) Publish(topic string, msg *bus.BusMessage) error {
	d.pubCh <- msg
	return nil
}
func (d *dummyBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) {
	ch, ok := d.chans[topic]
	if !ok {
		ch = make(chan *bus.BusMessage, 100)
		d.chans[topic] = ch
	}
	return ch, nil
}
func (d *dummyBus) Close() error { return nil }

func TestMonitorHeadless(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	b.chans["vram_update"] <- &bus.BusMessage{Target: "vram_updated"}
	time.Sleep(100 * time.Millisecond)
}

func TestMonitorFPSCounter(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})
	m.bus = b

	now := time.Now().Truncate(time.Second)
	m.stats.SetNowFunc(func() time.Time { return now })

	for range 60 {
		m.RecordFrame()
	}

	now = now.Add(1 * time.Second)

	ms := m.GetMonitorStats()
	if ms.FPS1s != 60.0 {
		t.Errorf("FPS1s = %f, want 60.0", ms.FPS1s)
	}
	if ms.FPS10s != 6.0 {
		t.Errorf("FPS10s = %f, want 6.0 (60 frames / 10s window)", ms.FPS10s)
	}
	if ms.FPS30s != 2.0 {
		t.Errorf("FPS30s = %f, want 2.0 (60 frames / 30s window)", ms.FPS30s)
	}
}

func TestMonitorGetStatsCommand(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// vram_update events no longer call RecordFrame in receiveLoop,
	// so these events only trigger handleVRAMEvent.
	vramCh := b.chans["vram_update"]
	for range 5 {
		vramCh <- &bus.BusMessage{Target: "vram_updated", Data: []byte{0, 0, 0, 0, 0, 1}}
	}
	time.Sleep(50 * time.Millisecond)

	monCh := b.chans["monitor"]
	monCh <- &bus.BusMessage{Target: "get_stats", Operation: bus.OpCommand}

	select {
	case msg := <-b.pubCh:
		if msg.Target != "stats_data" {
			t.Fatalf("expected stats_data, got %s", msg.Target)
		}
		var resp stats.MonitorStats
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no stats_data event received")
	}
}

func TestMonitorHeadlessMessageCount(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	vramCh := b.chans["vram_update"]
	for range 30 {
		vramCh <- &bus.BusMessage{
			Target: "vram_updated",
			Data:   []byte{0, 0, 0, 0, 0, 1},
		}
	}

	time.Sleep(1500 * time.Millisecond)

	ms := m.GetMonitorStats()
	if ms.FPS1s != 0 {
		t.Errorf("FPS1s = %f, want 0 (headless mode: onPaint never called)", ms.FPS1s)
	}
}

// --- Part 2 tests ---

type mockVRAMAccessor struct {
	index   []uint8
	color   []uint8
	width   int
	height  int
	palette [256][4]uint8
	dpg     int
	vpX     int16
	vpY     int16
}

func (m *mockVRAMAccessor) VRAMBuffer() []uint8           { return m.index }
func (m *mockVRAMAccessor) VRAMColorBuffer() []uint8       { return m.color }
func (m *mockVRAMAccessor) VRAMWidth() int                 { return m.width }
func (m *mockVRAMAccessor) VRAMHeight() int                { return m.height }
func (m *mockVRAMAccessor) VRAMPalette() [256][4]uint8     { return m.palette }
func (m *mockVRAMAccessor) DisplayPage() int               { return m.dpg }
func (m *mockVRAMAccessor) ViewportOffset() (int16, int16) { return m.vpX, m.vpY }

func TestMonitorDirectRefresh(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	mock := &mockVRAMAccessor{
		width:  256,
		height: 212,
		index:  make([]uint8, 256*212),
		color:  make([]uint8, 256*212*4),
	}
	mock.palette[5] = [4]uint8{50, 100, 150, 255}
	mock.index[0] = 5

	m.SetVRAMAccessor(mock)
	m.bus = b

	m.mu.Lock()
	m.dirty = true
	m.buildFrame()
	m.mu.Unlock()

	r, g, bv, a := m.rgba[0], m.rgba[1], m.rgba[2], m.rgba[3]
	if r != 50 || g != 100 || bv != 150 || a != 255 {
		t.Errorf("pixel = (%d,%d,%d,%d), want (50,100,150,255)", r, g, bv, a)
	}
}

func TestMonitorDirectRefreshDirectColor(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	mock := &mockVRAMAccessor{
		width:  256,
		height: 212,
		index:  make([]uint8, 256*212),
		color:  make([]uint8, 256*212*4),
	}
	mock.index[0] = directColorMarker
	mock.color[0] = 200
	mock.color[1] = 100
	mock.color[2] = 50
	mock.color[3] = 255

	m.SetVRAMAccessor(mock)
	m.bus = b

	m.mu.Lock()
	m.dirty = true
	m.buildFrame()
	m.mu.Unlock()

	r, g, bv, a := m.rgba[0], m.rgba[1], m.rgba[2], m.rgba[3]
	if r != 200 || g != 100 || bv != 50 || a != 255 {
		t.Errorf("pixel = (%d,%d,%d,%d), want (200,100,50,255)", r, g, bv, a)
	}
}

func TestMonitorPageFilter(t *testing.T) {
	b := newDummyBus()
	m := New(MonitorConfig{Headless: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	vramCh := b.chans["vram_update"]
	vramCh <- &bus.BusMessage{
		Target: "vram_updated",
		Data:   []byte{1, 0, 0, 0, 0, 42},
	}
	time.Sleep(50 * time.Millisecond)

	m.mu.Lock()
	val := m.vram[0]
	m.mu.Unlock()

	if val != 0 {
		t.Errorf("vram[0] = %d, want 0 (page 1 event should be ignored)", val)
	}
}
