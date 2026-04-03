package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	"github.com/axsh/neurom/internal/modules/io"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

func main() {
	headless := flag.Bool("headless", false, "Run without display monitor")
	tcpPort := flag.String("tcp-port", "5555", "TCP port for external bus connections")
	flag.Parse()

	log.Println("Starting Neurom (ARC Emulator)...")

	endpointInproc := "inproc://system-bus"
	endpointTCP := "tcp://127.0.0.1:" + *tcpPort

	b, err := bus.NewZMQBus(endpointInproc, endpointTCP)
	if err != nil {
		log.Fatalf("Failed to initialize system bus: %v", err)
	}
	defer b.Close()

	mgr := module.NewManager(b)

	// Register basic modules
	mgr.Register(cpu.New())
	mgr.Register(vram.New())
	mgr.Register(io.New())
	
	mon := monitor.New(monitor.MonitorConfig{
		Headless: *headless,
	})
	mgr.Register(mon)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		log.Fatalf("Failed to start modules: %v", err)
	}
	log.Println("All modules started successfully.")

	if *headless {
		// In headless mode, wait for either OS signal or bus shutdown command.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		sysCh, err := b.Subscribe("system")
		if err != nil {
			log.Fatalf("Failed to subscribe to system topic: %v", err)
		}

		select {
		case <-sigCh:
			log.Println("Received OS signal, initiating shutdown...")
		case msg := <-sysCh:
			if msg.Target == bus.TargetSystem && string(msg.Data) == bus.CmdShutdown {
				log.Println("Received shutdown command via bus, initiating shutdown...")
			}
		}
	} else {
		// UI main loop blocks here until app is finished
		log.Println("[Main] RunMain: entering UI event loop...")
		mon.RunMain()
		log.Println("[Main] RunMain: returned (window closed)")
	}

	log.Println("[Main] Shutting down VM... calling cancel()")
	cancel()
	log.Println("[Main] cancel() done. Calling mgr.StopAll()...")
	if err := mgr.StopAll(); err != nil {
		log.Printf("[Main] Errors during shutdown: %v", err)
	}
	log.Println("[Main] Shutdown complete. Exiting process.")
}
