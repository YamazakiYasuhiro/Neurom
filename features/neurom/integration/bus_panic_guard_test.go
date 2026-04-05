package integration

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestBusPanicGuard_InprocStartupNoPanic(t *testing.T) {
	for i := range 3 {
		b := bus.NewChannelBus()

		mgr := module.NewManager(b)
		mgr.Register(cpu.New())
		mgr.Register(vram.New())
		mgr.Register(monitor.New(monitor.MonitorConfig{Headless: true}))

		ctx, cancel := context.WithCancel(context.Background())

		if err := mgr.StartAll(ctx); err != nil {
			t.Fatalf("iteration %d: StartAll failed: %v", i, err)
		}

		time.Sleep(500 * time.Millisecond)

		cancel()
		if err := mgr.StopAll(); err != nil {
			t.Errorf("iteration %d: StopAll error: %v", i, err)
		}
		b.Close()
	}
}

func TestBusPanicGuard_LargeDataIntegrity(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()

	ch, err := b.Subscribe("test")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	largeData := make([]byte, 256*1024) // 256 KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	msg := &bus.BusMessage{
		Target:    "large_data",
		Operation: bus.OpWrite,
		Data:      largeData,
		Source:    "Test",
	}

	if err := b.Publish("test_topic", msg); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	select {
	case received := <-ch:
		if !bytes.Equal(received.Data, largeData) {
			t.Errorf("data mismatch: got len %d, want len %d", len(received.Data), len(largeData))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for large message")
	}
}
