package integration

import (
	"testing"
	"time"

	"github.com/axsh/neurom/arcproto"
	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/modules/vram"
)

func TestPageManagementIntegration(t *testing.T) {
	b, _, _, _ := setupEnhancementTestEnv(t)

	ch, err := b.Subscribe(arcproto.TopicVRAMUpdate)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPageCount, Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})

	timeout := time.After(2 * time.Second)
	found := false
	for {
		select {
		case msg := <-ch:
			if msg.Target == arcproto.EventPageCountChanged {
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

	ch, err := b.Subscribe(arcproto.TopicVRAMUpdate)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Add page 1
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPageCount, Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Draw on page 0 only
	pixels := make([]byte, 4)
	for i := range pixels {
		pixels[i] = 3
	}
	b.Publish(arcproto.TopicVRAM, blitIntegration(0, 0, 0, 2, 2, 0x00, pixels))
	time.Sleep(50 * time.Millisecond)

	for len(ch) > 0 {
		<-ch
	}

	// Read page 0: should have pattern
	readCmd := arcproto.ReadRect{Page: 0, X: 0, Y: 0, W: 2, H: 2}
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: readCmd.Target(), Operation: bus.OpCommand,
		Data: readCmd.Encode(), Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	got := false
	timeout := time.After(2 * time.Second)
	for !got {
		select {
		case msg := <-ch:
			if msg.Target == arcproto.EventRectData {
				e, err := arcproto.DecodeRectData(msg.Data)
				if err != nil {
					t.Fatalf("DecodeRectData() error = %v", err)
				}
				for _, px := range e.Pixels {
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
	readCmd.Page = 1
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: readCmd.Target(), Operation: bus.OpCommand,
		Data: readCmd.Encode(), Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	got = false
	timeout = time.After(2 * time.Second)
	for !got {
		select {
		case msg := <-ch:
			if msg.Target == arcproto.EventRectData {
				e, err := arcproto.DecodeRectData(msg.Data)
				if err != nil {
					t.Fatalf("DecodeRectData() error = %v", err)
				}
				for _, px := range e.Pixels {
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

	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPageCount, Operation: bus.OpCommand,
		Data: []byte{2}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Set palette
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPalette, Operation: bus.OpCommand,
		Data: []byte{1, 255, 0, 0, 255}, Source: "test",
	})
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPalette, Operation: bus.OpCommand,
		Data: []byte{2, 0, 255, 0, 255}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Draw idx 1 (red) on page 0
	b.Publish(arcproto.TopicVRAM, blitIntegration(0, 0, 0, 1, 1, 0x00, []byte{1}))
	// Draw idx 2 (green) on page 1
	b.Publish(arcproto.TopicVRAM, blitIntegration(1, 0, 0, 1, 1, 0x00, []byte{2}))
	time.Sleep(100 * time.Millisecond)

	// Display page 0: should see red
	r, _, _, _ := mon.GetPixel(0, 0)
	if r != 255 {
		t.Errorf("page 0 display: R = %d, want 255 (red)", r)
	}

	// Switch display to page 1
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetDisplayPage, Operation: bus.OpCommand,
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
	sizeCmd := arcproto.SetPageSize{Page: 0, W: 512, H: 512}
	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: sizeCmd.Target(), Operation: bus.OpCommand,
		Data: sizeCmd.Encode(), Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	// Blit at (400, 400) on the enlarged page
	b.Publish(arcproto.TopicVRAM, blitIntegration(0, 400, 400, 2, 2, 0x00, []byte{5, 5, 5, 5}))
	time.Sleep(50 * time.Millisecond)

	var f vram.Frame
	vramMod.Snapshot(&f)
	if f.Width != 512 || f.Height != 512 {
		t.Errorf("page 0 size = %dx%d, want 512x512", f.Width, f.Height)
	}

	if got := f.Index[400*f.Width+400]; got != 5 {
		t.Errorf("pixel at (400,400) = %d, want 5", got)
	}
}

func TestVRAMAccessorMonitorIntegration(t *testing.T) {
	b, vramMod, mon, _ := setupEnhancementTestEnv(t)

	mon.SetVRAMAccessor(vramMod)

	b.Publish(arcproto.TopicVRAM, &bus.BusMessage{
		Target: arcproto.TargetSetPalette, Operation: bus.OpCommand,
		Data: []byte{1, 255, 0, 0, 255}, Source: "test",
	})
	time.Sleep(50 * time.Millisecond)

	b.Publish(arcproto.TopicVRAM, blitIntegration(0, 5, 5, 1, 1, 0x00, []byte{1}))
	time.Sleep(100 * time.Millisecond)

	r, g, bVal, a := mon.GetPixel(5, 5)
	if r != 255 || g != 0 || bVal != 0 || a != 255 {
		t.Errorf("pixel = (%d,%d,%d,%d), want (255,0,0,255)", r, g, bVal, a)
	}
}
