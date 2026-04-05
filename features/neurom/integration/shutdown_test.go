package integration

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/cpu"
	iomod "github.com/axsh/neurom/internal/modules/io"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
)

// TestGracefulShutdownViaBus verifies that publishing a system shutdown
// message through the bus causes all modules to terminate gracefully
// without deadlocking.
func TestGracefulShutdownViaBus(t *testing.T) {
	b := bus.NewChannelBus()
	defer b.Close()

	mgr := module.NewManager(b)

	// Register all modules in headless mode.
	mgr.Register(cpu.New())
	mgr.Register(vram.New())
	mgr.Register(iomod.New())
	mgr.Register(monitor.New(monitor.MonitorConfig{Headless: true}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	// Allow modules to settle.
	time.Sleep(200 * time.Millisecond)

	// Publish shutdown command to the system topic.
	err := b.Publish("system", &bus.BusMessage{
		Target:    bus.TargetSystem,
		Operation: bus.OpCommand,
		Data:      []byte(bus.CmdShutdown),
		Source:    "Test",
	})
	if err != nil {
		t.Fatalf("Failed to publish shutdown command: %v", err)
	}

	// Allow time for the shutdown message to propagate.
	time.Sleep(200 * time.Millisecond)

	// StopAll should complete without deadlock within a reasonable timeout.
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- mgr.StopAll()
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			t.Errorf("StopAll returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StopAll timed out — possible deadlock in graceful shutdown")
	}
}
