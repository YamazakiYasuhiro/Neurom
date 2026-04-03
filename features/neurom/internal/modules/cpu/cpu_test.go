package cpu

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type dummyBus struct {
    ch chan *bus.BusMessage
}
func (d *dummyBus) Publish(topic string, msg *bus.BusMessage) error { 
    d.ch <- msg
    return nil 
}
func (d *dummyBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) { return nil, nil }
func (d *dummyBus) Close() error { return nil }

func TestCPUModule(t *testing.T) {
	b := &dummyBus{ch: make(chan *bus.BusMessage, 1)}
	c := New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx, b); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	select {
	case msg := <-b.ch:
		if msg.Source != "CPU" {
			t.Errorf("Expected source CPU, got %s", msg.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for CPU message")
	}
}
