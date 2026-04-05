package bus

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestChannelBus_Basic(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	ch, err := b.Subscribe("vram")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	msg := &BusMessage{
		Target:    "0x2000",
		Operation: OpWrite,
		Data:      []byte{0x01},
		Source:    "CPU",
	}

	if err := b.Publish("vram_update", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if received.Target != msg.Target {
			t.Errorf("expected target %s, got %s", msg.Target, received.Target)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestChannelBus_NoPanicOnStartup(t *testing.T) {
	for range 10 {
		b := NewChannelBus()
		if err := b.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	}
}

func TestChannelBus_SmallMessage(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	ch, err := b.Subscribe("data")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	smallData := make([]byte, 100)
	for i := range smallData {
		smallData[i] = byte(i)
	}
	msg := &BusMessage{Target: "small", Operation: OpWrite, Data: smallData, Source: "Test"}
	if err := b.Publish("data_topic", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if !bytes.Equal(received.Data, smallData) {
			t.Error("received data does not match original")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestChannelBus_LargeMessage(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	ch, err := b.Subscribe("big")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	largeData := make([]byte, 100*1024) // 100 KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	msg := &BusMessage{Target: "large", Operation: OpWrite, Data: largeData, Source: "Test"}
	if err := b.Publish("big_topic", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if !bytes.Equal(received.Data, largeData) {
			t.Errorf("data mismatch: got len %d, want len %d", len(received.Data), len(largeData))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestChannelBus_VeryLargeMessage(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	ch, err := b.Subscribe("huge")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	hugeData := make([]byte, 1024*1024) // 1 MB
	for i := range hugeData {
		hugeData[i] = byte(i % 256)
	}
	msg := &BusMessage{Target: "huge", Operation: OpWrite, Data: hugeData, Source: "Test"}
	if err := b.Publish("huge_topic", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if !bytes.Equal(received.Data, hugeData) {
			t.Errorf("data mismatch: got len %d, want len %d", len(received.Data), len(hugeData))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestChannelBus_Boundary(t *testing.T) {
	tests := []struct {
		name     string
		dataSize int
	}{
		{"empty_data", 0},
		{"one_byte", 1},
		{"256_bytes", 256},
		{"32KB", 32 * 1024},
		{"64KB", 64 * 1024},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewChannelBus()
			defer b.Close()

			ch, err := b.Subscribe(fmt.Sprintf("bound_%d", i))
			if err != nil {
				t.Fatalf("Subscribe failed: %v", err)
			}

			data := make([]byte, tt.dataSize)
			for j := range data {
				data[j] = byte(j % 256)
			}
			msg := &BusMessage{Target: "boundary", Operation: OpWrite, Data: data, Source: "Test"}
			topic := fmt.Sprintf("bound_%d_topic", i)
			if err := b.Publish(topic, msg); err != nil {
				t.Fatalf("Publish failed: %v", err)
			}

			select {
			case received := <-ch:
				if !bytes.Equal(received.Data, data) {
					t.Errorf("data mismatch: got len %d, want len %d", len(received.Data), len(data))
				}
			case <-time.After(time.Second):
				t.Fatal("timeout")
			}
		})
	}
}

func TestChannelBus_MultipleSubscribers(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	ch1, err := b.Subscribe("events")
	if err != nil {
		t.Fatalf("Subscribe 1 failed: %v", err)
	}
	ch2, err := b.Subscribe("events")
	if err != nil {
		t.Fatalf("Subscribe 2 failed: %v", err)
	}

	msg := &BusMessage{Target: "t", Operation: OpWrite, Data: []byte{1}, Source: "S"}
	if err := b.Publish("events_fire", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	for i, ch := range []<-chan *BusMessage{ch1, ch2} {
		select {
		case received := <-ch:
			if received.Target != "t" {
				t.Errorf("subscriber %d: wrong target", i)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timeout", i)
		}
	}
}
