package integration

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

func TestPageManagementIntegration(t *testing.T) {
	b, _, _, _ := setupEnhancementTestEnv(t)

	ch, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})

	timeout := time.After(2 * time.Second)
	found := false
	for {
		select {
		case msg := <-ch:
			if msg.Target == "page_count_changed" {
				found = true
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:
	if !found {
		t.Fatal("No page_count_changed event received")
	}
}

func TestPageDrawIsolationIntegration(t *testing.T) {
	b, vramMod, _, _ := setupEnhancementTestEnv(t)

	ch, err := b.Subscribe("vram_update")
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Add page 1
	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Draw on page 0 only
	pixels := make([]byte, 4)
	for i := range pixels {
		pixels[i] = 3
	}
	b.Publish("vram", blitIntegration(0, 0, 0, 2, 2, 0x00, pixels))
	time.Sleep(50 * time.Millisecond)

	for len(ch) > 0 {
		<-ch
	}

	// Read page 0: should have pattern
	readData := make([]byte, 9)
	readData[0] = 0
	binary.BigEndian.PutUint16(readData[1:], 0)
	binary.BigEndian.PutUint16(readData[3:], 0)
	binary.BigEndian.PutUint16(readData[5:], 2)
	binary.BigEndian.PutUint16(readData[7:], 2)
	b.Publish("vram", &bus.BusMessage{
		Target: "read_rect", Operation: bus.OpCommand, Data: readData, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	got := false
	timeout := time.After(2 * time.Second)
	for !got {
		select {
		case msg := <-ch:
			if msg.Target == "rect_data" {
				for _, px := range msg.Data[8:] {
					if px != 3 {
						t.Errorf("page 0 pixel = %d, want 3", px)
					}
				}
				got = true
			}
		case <-timeout:
			t.Fatal("No rect_data for page 0")
		}
	}

	// Read page 1: should be all zeros
	readData[0] = 1
	b.Publish("vram", &bus.BusMessage{
		Target: "read_rect", Operation: bus.OpCommand, Data: readData, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	got = false
	timeout = time.After(2 * time.Second)
	for !got {
		select {
		case msg := <-ch:
			if msg.Target == "rect_data" {
				for _, px := range msg.Data[8:] {
					if px != 0 {
						t.Errorf("page 1 pixel = %d, want 0", px)
					}
				}
				got = true
			}
		case <-timeout:
			t.Fatal("No rect_data for page 1")
		}
	}

	_ = vramMod
}

func TestPageDisplayIntegration(t *testing.T) {
	b, vramMod, mon, _ := setupEnhancementTestEnv(t)

	mon.SetVRAMAccessor(vramMod)

	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_count", Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Set palette
	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{1, 255, 0, 0, 255}, Source: "test",
	})
	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{2, 0, 255, 0, 255}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Draw idx 1 (red) on page 0
	b.Publish("vram", blitIntegration(0, 0, 0, 1, 1, 0x00, []byte{1}))
	// Draw idx 2 (green) on page 1
	b.Publish("vram", blitIntegration(1, 0, 0, 1, 1, 0x00, []byte{2}))
	time.Sleep(100 * time.Millisecond)

	// Display page 0: should see red
	r, _, _, _ := mon.GetPixel(0, 0)
	if r != 255 {
		t.Errorf("page 0 display: R = %d, want 255 (red)", r)
	}

	// Switch display to page 1
	b.Publish("vram", &bus.BusMessage{
		Target: "set_display_page", Operation: bus.OpCommand,
		Data: []byte{1}, Source: "test",
	})
	time.Sleep(100 * time.Millisecond)

	_, g, _, _ := mon.GetPixel(0, 0)
	if g != 255 {
		t.Errorf("page 1 display: G = %d, want 255 (green)", g)
	}
}

func TestPageSizeIntegration(t *testing.T) {
	b, vramMod, _, _ := setupEnhancementTestEnv(t)

	// Resize page 0 to 512x512
	data := make([]byte, 5)
	data[0] = 0
	binary.BigEndian.PutUint16(data[1:], 512)
	binary.BigEndian.PutUint16(data[3:], 512)
	b.Publish("vram", &bus.BusMessage{
		Target: "set_page_size", Operation: bus.OpCommand, Data: data, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Blit at (400, 400) on the enlarged page
	b.Publish("vram", blitIntegration(0, 400, 400, 2, 2, 0x00, []byte{5, 5, 5, 5}))
	time.Sleep(50 * time.Millisecond)

	if vramMod.VRAMWidth() != 512 || vramMod.VRAMHeight() != 512 {
		t.Errorf("page 0 size = %dx%d, want 512x512", vramMod.VRAMWidth(), vramMod.VRAMHeight())
	}

	buf := vramMod.VRAMBuffer()
	if buf[400*512+400] != 5 {
		t.Errorf("pixel at (400,400) = %d, want 5", buf[400*512+400])
	}
}

func TestVRAMAccessorMonitorIntegration(t *testing.T) {
	b, vramMod, mon, _ := setupEnhancementTestEnv(t)

	mon.SetVRAMAccessor(vramMod)

	b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{1, 255, 0, 0, 255}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	b.Publish("vram", blitIntegration(0, 5, 5, 1, 1, 0x00, []byte{1}))
	time.Sleep(100 * time.Millisecond)

	r, g, bVal, a := mon.GetPixel(5, 5)
	if r != 255 || g != 0 || bVal != 0 || a != 255 {
		t.Errorf("pixel = (%d,%d,%d,%d), want (255,0,0,255)", r, g, bVal, a)
	}
}
