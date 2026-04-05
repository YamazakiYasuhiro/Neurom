package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	"github.com/axsh/neurom/internal/modules/io"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
	"github.com/axsh/neurom/internal/statsserver"
)

func main() {
	headless := flag.Bool("headless", false, "Run without display monitor")
	noTCP := flag.Bool("no-tcp", false, "Disable TCP bridge for external connections")
	tcpPort := flag.String("tcp-port", "5555", "TCP port for external bus connections")
	cpuWorkers := flag.Int("cpu", 1, "Number of VRAM worker goroutines (1-256)")
	vramStrategy := flag.String("vram-strategy", "dynamic", "VRAM parallelization strategy: static or dynamic")
	statsPort := flag.String("stats-port", "", "HTTP port for stats endpoint (disabled if empty)")
	flag.Parse()

	log.Println("Starting Neurom (ARC Emulator)...")

	channelBus := bus.NewChannelBus()
	defer channelBus.Close()

	var tcpBridge *bus.TCPBridge
	if !*noTCP {
		tcpBridge = bus.NewTCPBridge(bus.TCPBridgeConfig{
			Bus:         channelBus,
			TCPEndpoint: "tcp://127.0.0.1:" + *tcpPort,
		})
		if err := tcpBridge.Start(); err != nil {
			log.Printf("TCP bridge failed to start: %v", err)
		}
	}
	defer func() {
		if tcpBridge != nil {
			tcpBridge.Close()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := module.NewManager(channelBus)

	vramMod := vram.New(vram.Config{Workers: *cpuWorkers, Strategy: *vramStrategy})
	mgr.Register(cpu.New())
	mgr.Register(vramMod)
	mgr.Register(io.New())

	mon := monitor.New(monitor.MonitorConfig{
		Headless: *headless,
		OnClose:  cancel,
	})
	mon.SetVRAMAccessor(vramMod)
	mgr.Register(mon)

	if err := mgr.StartAll(ctx); err != nil {
		log.Fatalf("Failed to start modules: %v", err)
	}
	log.Println("All modules started successfully.")

	if *statsPort != "" {
		ss := statsserver.New(vramMod, mon, *statsPort)
		if err := ss.Start(); err != nil {
			log.Printf("Stats server failed to start: %v", err)
		} else {
			log.Printf("Stats server listening on %s", ss.Addr())
			defer func() {
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutCancel()
				ss.Shutdown(shutCtx)
			}()
		}
	}

	if *headless {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		sysCh, err := channelBus.Subscribe("system")
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
