package bus

import (
	"testing"
	"time"
)

func TestZMQBus(t *testing.T) {
	bus, err := NewZMQBus("inproc://test-bus")
	if err != nil {
		t.Fatalf("Failed to create ZMQBus: %v", err)
	}
	defer bus.Close()

	ch, err := bus.Subscribe("vram")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Wait for subscription to propagate in ZeroMQ
	time.Sleep(100 * time.Millisecond)

	msg := &BusMessage{
		Target:    "0x2000",
		Operation: OpWrite,
		Data:      []byte{0x01},
		Source:    "CPU",
	}

	if err := bus.Publish("vram_update", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if received.Target != msg.Target {
			t.Errorf("Expected target %s, got %s", msg.Target, received.Target)
		}
	case <-time.After(time.Second):
		t.Fatal("Timeout waiting for message")
	}
}
