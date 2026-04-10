package integration

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestPaletteUpdate(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v := vram.New()
	m := monitor.New(monitor.MonitorConfig{Headless: true})

	mgr := module.NewManager(b)
	mgr.Register(v)
	mgr.Register(m)

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // Wait for subscriptions

	// 1. Send set_palette message to change Index 0x01 to Red (255, 0, 0)
	if err := b.Publish("vram", &bus.BusMessage{
		Target:    "set_palette",
		Operation: bus.OpCommand,
		Data:      []byte{0x01, 255, 0, 0},
	}); err != nil {
		t.Fatalf("Failed to publish set_palette: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // Wait for palette propagation

	// 2. Send draw_pixel at X=10, Y=10 with color index 0x01
	if err := b.Publish("vram", &bus.BusMessage{
		Target:    "draw_pixel",
		Operation: bus.OpCommand,
		Data:      []byte{0x00, 0x00, 10, 0x00, 10, 0x01}, // page:0, X:10, Y:10, p:1
	}); err != nil {
		t.Fatalf("Failed to publish draw_pixel: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // Wait for pixel rendering

	// Get RGBA pixel at X:10, Y:10
	red, green, blue, _ := m.GetPixel(10, 10)

	if red != 255 || green != 0 || blue != 0 {
		t.Errorf("Expected pixel to be Red (255, 0, 0), but got (%d, %d, %d)", red, green, blue)
	}
}
