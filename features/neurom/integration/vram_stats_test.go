package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestVRAMStatsIntegration(t *testing.T) {
	b := bus.NewChannelBus()
	mgr := module.NewManager(b)

	vramMod := vram.New()
	mgr.Register(vramMod)

	updateCh, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe to vram_update: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	// Send draw_pixel 20 times
	for range 20 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target:    "draw_pixel",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		})
	}

	// Send set_palette 5 times
	for range 5 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target:    "set_palette",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0xFF, 0x00, 0x00},
		})
	}

	// Wait for messages to be processed + window rotation
	time.Sleep(1500 * time.Millisecond)

	// Drain any queued vram_update events
	drainLoop:
	for {
		select {
		case <-updateCh:
		default:
			break drainLoop
		}
	}

	// Request stats
	_ = b.Publish("vram", &bus.BusMessage{
		Target:    "get_stats",
		Operation: bus.OpCommand,
	})

	// Wait for stats_data response
	var statsMsg *bus.BusMessage
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-updateCh:
			if msg.Target == "stats_data" {
				statsMsg = msg
			}
		case <-deadline:
			if statsMsg == nil {
				t.Fatal("Timed out waiting for stats_data event")
			}
		}
		if statsMsg != nil {
			break
		}
	}

	var resp struct {
		Commands map[string]struct {
			Last1s struct {
				Count int64 `json:"count"`
				AvgNs int64 `json:"avg_ns"`
				MaxNs int64 `json:"max_ns"`
			} `json:"last_1s"`
			Last10s struct {
				Count int64 `json:"count"`
			} `json:"last_10s"`
			Last30s struct {
				Count int64 `json:"count"`
			} `json:"last_30s"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(statsMsg.Data, &resp); err != nil {
		t.Fatalf("Failed to unmarshal stats JSON: %v\ndata: %s", err, string(statsMsg.Data))
	}

	dp, ok := resp.Commands["draw_pixel"]
	if !ok {
		t.Fatal("draw_pixel not found in stats response")
	}
	if dp.Last10s.Count != 20 {
		t.Errorf("draw_pixel Last10s.Count = %d, want 20", dp.Last10s.Count)
	}

	sp, ok := resp.Commands["set_palette"]
	if !ok {
		t.Fatal("set_palette not found in stats response")
	}
	if sp.Last10s.Count != 5 {
		t.Errorf("set_palette Last10s.Count = %d, want 5", sp.Last10s.Count)
	}

	cancel()
	if err := mgr.StopAll(); err != nil {
		t.Errorf("StopAll returned error: %v", err)
	}
	_ = b.Close()
}
