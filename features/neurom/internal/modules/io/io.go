package io

import (
	"context"
	"log"
	"sync"

	"github.com/axsh/neurom/internal/bus"
)

type IOModule struct {
	wg sync.WaitGroup
}

func New() *IOModule {
	return &IOModule{}
}

func (i *IOModule) Name() string {
	return "IO"
}

func (i *IOModule) Start(ctx context.Context, b bus.Bus) error {
	sysCh, err := b.Subscribe("system")
	if err != nil {
		return err
	}

	i.wg.Add(1)
	go func() {
		defer i.wg.Done()
		i.run(ctx, sysCh)
	}()
	return nil
}

func (i *IOModule) Stop() error {
	log.Println("[IO] Stop: waiting for run goroutine...")
	i.wg.Wait()
	log.Println("[IO] Stop: run goroutine finished.")
	return nil
}

func (i *IOModule) run(ctx context.Context, sysCh <-chan *bus.BusMessage) {
	for {
		select {
		case <-ctx.Done():
			log.Println("[IO] run: ctx.Done received, exiting")
			return
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("[IO] run: shutdown command received, exiting")
				return
			}
		}
	}
}
