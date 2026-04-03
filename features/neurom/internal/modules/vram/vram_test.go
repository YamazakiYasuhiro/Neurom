package vram

import (
    "testing"
    "time"

    "github.com/axsh/neurom/internal/bus"
)

type testBus struct {
    ch chan *bus.BusMessage
}
func (b *testBus) Publish(topic string, msg *bus.BusMessage) error {
    b.ch <- msg
    return nil
}
func (b *testBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) {
    ch := make(chan *bus.BusMessage, 1)
    return ch, nil
}
func (b *testBus) Close() error { return nil }

func TestVRAMModule(t *testing.T) {
    b := &testBus{ch: make(chan *bus.BusMessage, 10)}
    v := New()
    
    // Simulate direct call run logic or test handleMessage
    v.bus = b
    
    msg := &bus.BusMessage{
        Target: "draw_pixel",
        Operation: bus.OpCommand,
        Data: []byte{0x00, 0x01, 0x00, 0x02, 0x0F}, // x=1, y=2, p=15
    }
    
    v.handleMessage(msg)
    
    if v.buffer[2*v.width+1] != 15 {
        t.Errorf("Expected pixel to be 15, got %d", v.buffer[2*v.width+1])
    }
    
    select {
    case out := <-b.ch:
        if out.Target != "vram_updated" {
            t.Errorf("Expected vram_updated, got %s", out.Target)
        }
    case <-time.After(time.Second):
        t.Fatal("No published event")
    }
}
