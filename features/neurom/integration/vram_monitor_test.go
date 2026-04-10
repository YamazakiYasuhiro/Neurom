package integration

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestVRAMMonitorIntegration(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()

	mgr := module.NewManager(b)
	
	// Register modules
	mgr.Register(cpu.New())
	mgr.Register(vram.New())
	mgr.Register(monitor.New(monitor.MonitorConfig{Headless: true}))

	// Subscribe to internal bus from outside to observe
	ch, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	// We expect the CPU to eventually send draw_pixel commands 
	// which VRAM receives, updates its buffer, and outputs vram_update
	received := false
waitLoop:
	for i := 0; i < 5; i++ {
		select {
		case msg := <-ch:
			// Just checking message topic instead of Source depending on how it's matched
			// since ZeroMQ Pub/Sub filters by topics (here msg is the payload struct)
			if msg.Source == "VRAM" {
				received = true
				break waitLoop
			}
		case <-time.After(500 * time.Millisecond):
			// wait 
		}
	}

	if !received {
		t.Fatal("Failed to receive vram_update message from VRAM via integration test")
	}

	cancel()
	if err := mgr.StopAll(); err != nil {
		t.Errorf("StopAll returned error: %v", err)
	}
}
