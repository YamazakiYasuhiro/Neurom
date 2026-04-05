package bus

import (
	"testing"
	"time"
)

func TestTCPBridge_StartStop(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	bridge := NewTCPBridge(TCPBridgeConfig{
		Bus:         b,
		TCPEndpoint: "tcp://127.0.0.1:15550",
		MaxRestarts: 3,
	})

	if err := bridge.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := bridge.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestTCPBridge_PanicRecovery(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	bridge := NewTCPBridge(TCPBridgeConfig{
		Bus:         b,
		TCPEndpoint: "tcp://127.0.0.1:15551",
		MaxRestarts: 5,
	})

	if err := bridge.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	bridge.mu.Lock()
	bridge.testPanicOnRecv = true
	bridge.mu.Unlock()

	// Trigger the panic by publishing a message
	_ = b.Publish("test_topic", &BusMessage{
		Target: "test", Operation: OpWrite, Data: []byte{1}, Source: "T",
	})

	time.Sleep(1 * time.Second)

	bridge.mu.Lock()
	restarts := bridge.restarts
	bridge.mu.Unlock()

	if restarts < 1 {
		t.Errorf("expected at least 1 restart after panic, got %d", restarts)
	}

	if err := bridge.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestTCPBridge_MaxRestartsExceeded(t *testing.T) {
	b := NewChannelBus()
	defer b.Close()

	bridge := NewTCPBridge(TCPBridgeConfig{
		Bus:         b,
		TCPEndpoint: "tcp://127.0.0.1:15552",
		MaxRestarts: 2,
	})

	// Override run to always panic
	bridge.testAlwaysPanic = true

	if err := bridge.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait enough for the bridge to exhaust restarts
	time.Sleep(3 * time.Second)

	bridge.mu.Lock()
	restarts := bridge.restarts
	bridge.mu.Unlock()

	if restarts < 2 {
		t.Errorf("expected at least 2 restarts, got %d", restarts)
	}

	if err := bridge.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}
