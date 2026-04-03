package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type dummyBus struct {
    ch chan *bus.BusMessage
}
func (d *dummyBus) Publish(topic string, msg *bus.BusMessage) error { return nil }
func (d *dummyBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) { 
    return d.ch, nil 
}
func (d *dummyBus) Close() error { return nil }

func TestMonitorHeadless(t *testing.T) {
	b := &dummyBus{ch: make(chan *bus.BusMessage, 10)}
	m := New(MonitorConfig{Headless: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	b.ch <- &bus.BusMessage{Target: "vram_updated"}

	time.Sleep(100 * time.Millisecond)
}
