package vram

import (
	"context"
	"log"
	"sync"

	"github.com/axsh/neurom/internal/bus"
)

type VRAMModule struct {
	mu      sync.RWMutex
	width   int
	height  int
	color   int
	buffer  []uint8
	palette [256][3]uint8
	bus     bus.Bus
	wg      sync.WaitGroup
}

func New() *VRAMModule {
	return &VRAMModule{
		width:  256,
		height: 212,
		color:  8,
		buffer: make([]uint8, 256*212),
	}
}

func (v *VRAMModule) Name() string {
	return "VRAM"
}

func (v *VRAMModule) Start(ctx context.Context, b bus.Bus) error {
	v.bus = b
	ch, err := b.Subscribe("vram")
	if err != nil {
		return err
	}

	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}

	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		v.run(ctx, ch, sysCh)
	}()
	return nil
}

func (v *VRAMModule) Stop() error {
	log.Println("[VRAM] Stop: waiting for run goroutine...")
	v.wg.Wait()
	log.Println("[VRAM] Stop: run goroutine finished.")
	return nil
}

func (v *VRAMModule) run(ctx context.Context, ch <-chan *bus.BusMessage, sysCh <-chan *bus.BusMessage) {
	for {
		select {
		case <-ctx.Done():
			log.Println("[VRAM] run: ctx.Done received, exiting")
			return
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[VRAM] run: shutdown command received, exiting")
				return
			}
		case msg := <-ch:
			v.handleMessage(msg)
		}
	}
}

func (v *VRAMModule) handleMessage(msg *bus.BusMessage) {
	if msg.Operation != bus.OpCommand {
		return
	}
	
	v.mu.Lock()
	defer v.mu.Unlock()

	switch msg.Target {
	case "mode":
		v.buffer = make([]uint8, v.width*v.height)
		v.bus.Publish("vram_update", &bus.BusMessage{
			Target:    "mode_changed",
			Operation: bus.OpCommand,
			Data:      nil,
			Source:    v.Name(),
		})
	case "draw_pixel":
		// Format: [x:(uint16)][y:(uint16)][p:(uint8)]
		if len(msg.Data) >= 5 {
			x := int(msg.Data[0])<<8 | int(msg.Data[1])
			y := int(msg.Data[2])<<8 | int(msg.Data[3])
			p := msg.Data[4]
			if x < v.width && y < v.height {
				v.buffer[y*v.width+x] = p
				v.bus.Publish("vram_update", &bus.BusMessage{
					Target:    "vram_updated",
					Operation: bus.OpCommand,
					Data:      msg.Data,
					Source:    v.Name(),
				})
			}
		}
	case "set_palette":
		if len(msg.Data) >= 4 {
			index := msg.Data[0]
			r, g, b := msg.Data[1], msg.Data[2], msg.Data[3]
			v.palette[index] = [3]uint8{r, g, b}
			v.bus.Publish("vram_update", &bus.BusMessage{
				Target:    "palette_updated",
				Operation: bus.OpCommand,
				Data:      msg.Data,
				Source:    v.Name(),
			})
		}
	}
}
