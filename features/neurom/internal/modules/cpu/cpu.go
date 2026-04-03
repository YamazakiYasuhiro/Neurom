package cpu

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type CPUModule struct {
	wg sync.WaitGroup
}

func New() *CPUModule {
	return &CPUModule{}
}

func (c *CPUModule) Name() string {
	return "CPU"
}

func (c *CPUModule) Start(ctx context.Context, b bus.Bus) error {
	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.run(ctx, b, sysCh)
	}()
	return nil
}

func (c *CPUModule) Stop() error {
	log.Println("[CPU] Stop: waiting for run goroutine...")
	c.wg.Wait()
	log.Println("[CPU] Stop: run goroutine finished.")
	return nil
}

func (c *CPUModule) run(ctx context.Context, b bus.Bus, sysCh <-chan *bus.BusMessage) {
	// Initialize display via VRAM
	_ = b.Publish("vram", &bus.BusMessage{
		Target:    "mode",
		Operation: bus.OpCommand,
		Data:      []byte{0x00, 0x01, 0x00},
		Source:    c.Name(),
	})

	// Wait briefly for VRAM/Monitor initialization
	time.Sleep(300 * time.Millisecond)

	// Demo: draw a 128x128 rainbow scroll block at screen center (indices 1-16)
	for y := 42; y < 42+128; y++ {
		p := byte(((y - 42) / 8) % 16) + 1
		hiY, loY := byte((y>>8)&0xFF), byte(y&0xFF)

		for x := 64; x < 64+128; x++ {
			hiX, loX := byte((x>>8)&0xFF), byte(x&0xFF)
			_ = b.Publish("vram", &bus.BusMessage{
				Target:    "draw_pixel",
				Operation: bus.OpCommand,
				Data:      []byte{hiX, loX, hiY, loY, p},
				Source:    c.Name(),
			})
		}
	}

	// Start palette animation (color cycling)
	ticker := time.NewTicker(33 * time.Millisecond) // ~30FPS
	defer ticker.Stop()

	offset := 0
	for {
		select {
		case <-ctx.Done():
			log.Println("[CPU] run: ctx.Done received, exiting")
			return
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[CPU] run: shutdown command received, exiting")
				return
			}
		case <-ticker.C:
			// Shift palette indices 1-16 to create scrolling gradient effect
			for i := 1; i <= 16; i++ {
				hue := float64((i*16+offset)%256) / 256.0
				r, g, bColor := hsvToRGB(hue, 1.0, 1.0)
				_ = b.Publish("vram", &bus.BusMessage{
					Target:    "set_palette",
					Operation: bus.OpCommand,
					Data:      []byte{byte(i), r, g, bColor},
					Source:    c.Name(),
				})
			}
			offset = (offset - 4) % 256
			if offset < 0 {
				offset += 256
			}
		}
	}
}

// hsvToRGB converts HSV color values to RGB bytes.
func hsvToRGB(h, s, v float64) (byte, byte, byte) {
	i := int(h * 6)
	f := h*6 - float64(i)
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)

	var r, g, b float64
	switch i % 6 {
	case 0: r, g, b = v, t, p
	case 1: r, g, b = q, v, p
	case 2: r, g, b = p, v, t
	case 3: r, g, b = p, q, v
	case 4: r, g, b = t, p, v
	case 5: r, g, b = v, p, q
	}
	return byte(r * 255), byte(g * 255), byte(b * 255)
}
