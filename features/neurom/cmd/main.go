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
		// Wait for interrupt signal if headless
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
	} else {
		// UI main loop blocks here until app is finished
		mon.RunMain()
	}

	log.Println("Shutting down VM...")
	if err := mgr.StopAll(); err != nil {
		log.Printf("Errors during shutdown: %v", err)
	}
	log.Println("Shutdown complete.")
}
