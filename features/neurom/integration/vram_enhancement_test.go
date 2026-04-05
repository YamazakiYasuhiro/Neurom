package integration

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestDemoProgram(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()

	vramMod := vram.New()
	mon := monitor.New(monitor.MonitorConfig{Headless: true})
	mon.SetVRAMAccessor(vramMod)

	mgr := module.NewManager(b)
	mgr.Register(cpu.New())
	mgr.Register(vramMod)
	mgr.Register(mon)

	ch, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// 7 scenes x 3s = 21s + buffer
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	received := false
	timeout := time.After(5 * time.Second)
waitLoop:
	for {
		select {
		case <-ch:
			received = true
			break waitLoop
		case <-timeout:
			break waitLoop
		}
	}

	if !received {
		t.Fatal("No vram_update messages received from demo program")
	}

	// Let full demo run for all 7 scenes
	time.Sleep(22 * time.Second)

	cancel()
	if err := mgr.StopAll(); err != nil {
		t.Errorf("StopAll returned error: %v", err)
	}
}

func setupEnhancementTestEnv(t *testing.T) (*bus.ChannelBus, *vram.VRAMModule, *monitor.MonitorModule, context.CancelFunc) {
	t.Helper()
	b := bus.NewChannelBus()
	vramMod := vram.New()
	mon := monitor.New(monitor.MonitorConfig{Headless: true})

	mgr := module.NewManager(b)
	mgr.Register(vramMod)
	mgr.Register(mon)

	ctx, cancel := context.WithCancel(context.Background())
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	t.Cleanup(func() {
		cancel()
		mgr.StopAll()
		b.Close()
	})

	return b, vramMod, mon, cancel
}

func blitIntegration(page uint8, x, y, w, h uint16, blend uint8, pixels []byte) *bus.BusMessage {
	data := make([]byte, 10+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], w)
	binary.BigEndian.PutUint16(data[7:], h)
	data[9] = blend
	copy(data[10:], pixels)
	return &bus.BusMessage{Target: "blit_rect", Operation: bus.OpCommand, Data: data, Source: "test"}
}

func TestBlitAndReadRect(t *testing.T) {
	b, _, _, _ := setupEnhancementTestEnv(t)

	ch, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{5, 50, 100, 150, 255}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	pixels := make([]byte, 16)
	for i := range pixels {
		pixels[i] = 5
	}
	b.Publish("vram", blitIntegration(0, 10, 20, 4, 4, 0x00, pixels))
	time.Sleep(50 * time.Millisecond)

	for len(ch) > 0 {
		<-ch
	}

	// read_rect: [page:u8][x:u16][y:u16][w:u16][h:u16]
	readData := make([]byte, 9)
	readData[0] = 0
	binary.BigEndian.PutUint16(readData[1:], 10)
	binary.BigEndian.PutUint16(readData[3:], 20)
	binary.BigEndian.PutUint16(readData[5:], 4)
	binary.BigEndian.PutUint16(readData[7:], 4)
	b.Publish("vram", &bus.BusMessage{
		Target: "read_rect", Operation: bus.OpCommand, Data: readData, Source: "test",
	})

	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-ch:
			if msg.Target == "rect_data" {
				respPixels := msg.Data[8:]
				for i, px := range respPixels {
					if px != 5 {
						t.Errorf("pixel[%d] = %d, want 5", i, px)
					}
				}
				return
			}
		case <-timeout:
			t.Fatal("No rect_data response received")
		}
	}
}

func TestAlphaBlendPipeline(t *testing.T) {
	b, vramMod, _, _ := setupEnhancementTestEnv(t)

	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{1, 255, 0, 0, 255}, Source: "test",
	})
	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{2, 0, 0, 255, 128}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	b.Publish("vram", blitIntegration(0, 0, 0, 2, 2, 0x00, []byte{1, 1, 1, 1}))
	time.Sleep(50 * time.Millisecond)
	b.Publish("vram", blitIntegration(0, 0, 0, 2, 2, 0x01, []byte{2, 2, 2, 2}))
	time.Sleep(100 * time.Millisecond)

	cb := vramMod.VRAMColorBuffer()
	r, g, bVal := cb[0], cb[1], cb[2]

	if r < 120 || r > 135 {
		t.Errorf("R = %d, want ~127", r)
	}
	if g != 0 {
		t.Errorf("G = %d, want 0", g)
	}
	if bVal < 120 || bVal > 135 {
		t.Errorf("B = %d, want ~128", bVal)
	}
}
