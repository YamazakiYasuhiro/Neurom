package integration

import (
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func setupMultiCoreEnv(t *testing.T, workers int) (*bus.ChannelBus, *vram.VRAMModule, context.CancelFunc) {
	t.Helper()
	b := bus.NewChannelBus()
	vramMod := vram.New(vram.Config{Workers: workers})
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
	return b, vramMod, cancel
}

func blitMultiCore(page uint8, x, y, w, h uint16, blend vram.BlendMode, pixels []byte) *bus.BusMessage {
	data := make([]byte, 10+len(pixels))
	data[0] = page
	binary.BigEndian.PutUint16(data[1:], x)
	binary.BigEndian.PutUint16(data[3:], y)
	binary.BigEndian.PutUint16(data[5:], w)
	binary.BigEndian.PutUint16(data[7:], h)
	data[9] = byte(blend)
	copy(data[10:], pixels)
	return &bus.BusMessage{Target: "blit_rect", Operation: bus.OpCommand, Data: data}
}

func TestMultiCoreClearAndBlit(t *testing.T) {
	b1, vram1, _ := setupMultiCoreEnv(t, 1)
	b4, vram4, _ := setupMultiCoreEnv(t, 4)

	for i := range 16 {
		palData := []byte{uint8(i), uint8(i * 16), uint8(i * 8), uint8(i * 4), 255}
		b1.Publish("vram", &bus.BusMessage{Target: "set_palette", Operation: bus.OpCommand, Data: palData})
		b4.Publish("vram", &bus.BusMessage{Target: "set_palette", Operation: bus.OpCommand, Data: palData})
	}
	time.Sleep(50 * time.Millisecond)

	b1.Publish("vram", &bus.BusMessage{Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 3}})
	b4.Publish("vram", &bus.BusMessage{Target: "clear_vram", Operation: bus.OpCommand, Data: []byte{0, 3}})
	time.Sleep(50 * time.Millisecond)

	pixels := make([]byte, 32*32)
	for i := range pixels {
		pixels[i] = uint8(i % 16)
	}
	b1.Publish("vram", blitMultiCore(0, 10, 10, 32, 32, vram.BlendReplace, pixels))
	b4.Publish("vram", blitMultiCore(0, 10, 10, 32, 32, vram.BlendReplace, pixels))
	time.Sleep(100 * time.Millisecond)

	ch1, _ := b1.Subscribe("vram_update")
	ch4, _ := b4.Subscribe("vram_update")

	readMsg := &bus.BusMessage{Target: "read_rect", Operation: bus.OpCommand, Data: make([]byte, 9)}
	readMsg.Data[0] = 0
	binary.BigEndian.PutUint16(readMsg.Data[1:], 0)
	binary.BigEndian.PutUint16(readMsg.Data[3:], 0)
	binary.BigEndian.PutUint16(readMsg.Data[5:], 256)
	binary.BigEndian.PutUint16(readMsg.Data[7:], 212)

	b1.Publish("vram", readMsg)
	b4.Publish("vram", &bus.BusMessage{Target: "read_rect", Operation: bus.OpCommand, Data: readMsg.Data})
	time.Sleep(200 * time.Millisecond)

	var data1, data4 []byte
	timeout := time.After(3 * time.Second)
	for data1 == nil || data4 == nil {
		select {
		case msg := <-ch1:
			if msg.Target == "rect_data" {
				data1 = msg.Data
			}
		case msg := <-ch4:
			if msg.Target == "rect_data" {
				data4 = msg.Data
			}
		case <-timeout:
			t.Fatal("timeout waiting for rect_data")
		}
	}

	if len(data1) != len(data4) {
		t.Fatalf("data length mismatch: %d vs %d", len(data1), len(data4))
	}
	for i := range data1 {
		if data1[i] != data4[i] {
			t.Fatalf("data mismatch at byte %d: %d vs %d", i, data1[i], data4[i])
		}
	}

	buf1 := vram1.VRAMColorBuffer()
	buf4 := vram4.VRAMColorBuffer()
	if len(buf1) != len(buf4) {
		t.Fatalf("color buffer length mismatch: %d vs %d", len(buf1), len(buf4))
	}
	for i := range buf1 {
		if buf1[i] != buf4[i] {
			t.Fatalf("color buffer mismatch at byte %d: %d vs %d", i, buf1[i], buf4[i])
		}
	}
}

func TestMultiCoreBlendModes(t *testing.T) {
	modes := []struct {
		name  string
		blend vram.BlendMode
	}{
		{"Alpha", vram.BlendAlpha},
		{"Additive", vram.BlendAdditive},
		{"Multiply", vram.BlendMultiply},
		{"Screen", vram.BlendScreen},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			b1, vram1, _ := setupMultiCoreEnv(t, 1)
			b4, vram4, _ := setupMultiCoreEnv(t, 4)

			for i := range 16 {
				palData := []byte{uint8(i), uint8(100 + i*10), uint8(50 + i*5), uint8(i * 15), 200}
				b1.Publish("vram", &bus.BusMessage{Target: "set_palette", Operation: bus.OpCommand, Data: palData})
				b4.Publish("vram", &bus.BusMessage{Target: "set_palette", Operation: bus.OpCommand, Data: palData})
			}
			time.Sleep(50 * time.Millisecond)

			bg := make([]byte, 20*20)
			for i := range bg {
				bg[i] = 5
			}
			b1.Publish("vram", blitMultiCore(0, 0, 0, 20, 20, vram.BlendReplace, bg))
			b4.Publish("vram", blitMultiCore(0, 0, 0, 20, 20, vram.BlendReplace, bg))
			time.Sleep(50 * time.Millisecond)

			fg := make([]byte, 20*20)
			for i := range fg {
				fg[i] = 10
			}
			b1.Publish("vram", blitMultiCore(0, 0, 0, 20, 20, tc.blend, fg))
			b4.Publish("vram", blitMultiCore(0, 0, 0, 20, 20, tc.blend, fg))
			time.Sleep(100 * time.Millisecond)

			buf1 := vram1.VRAMColorBuffer()
			buf4 := vram4.VRAMColorBuffer()
			for i := range buf1 {
				if buf1[i] != buf4[i] {
					t.Fatalf("color buffer mismatch at byte %d: %d vs %d", i, buf1[i], buf4[i])
				}
			}
		})
	}
}

func TestMultiCoreShutdown(t *testing.T) {
	b := bus.NewChannelBus()
	vramMod := vram.New(vram.Config{Workers: 4})
	mon := monitor.New(monitor.MonitorConfig{Headless: true})

	mgr := module.NewManager(b)
	mgr.Register(vramMod)
	mgr.Register(mon)

	ctx, cancel := context.WithCancel(context.Background())
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	cancel()

	done := make(chan struct{})
	go func() {
		mgr.StopAll()
		b.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out — possible goroutine leak or hang")
	}
}
