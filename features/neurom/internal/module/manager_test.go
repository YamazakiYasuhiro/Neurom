package module

import (
	"context"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
)

type dummyBus struct{}

func (d *dummyBus) Publish(topic string, msg *bus.BusMessage) error       { return nil }
func (d *dummyBus) Subscribe(topic string) (<-chan *bus.BusMessage, error) { return nil, nil }
func (d *dummyBus) Close() error                                          { return nil }

type testModule struct {
	started bool
	stopped bool
}

func (t *testModule) Name() string { return "test" }
func (t *testModule) Start(ctx context.Context, b bus.Bus) error {
	t.started = true
	return nil
}
func (t *testModule) Stop() error {
	t.stopped = true
	return nil
}

func TestManagerLifecycle(t *testing.T) {
	b := &dummyBus{}
	m := NewManager(b)

	mod1 := &testModule{}
	mod2 := &testModule{}

	m.Register(mod1)
	m.Register(mod2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}

	if !mod1.started || !mod2.started {
		t.Error("Not all modules started")
	}

	if err := m.StopAll(); err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}

	if !mod1.stopped || !mod2.stopped {
		t.Error("Not all modules stopped")
	}

	// Double start test
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("Second StartAll failed: %v", err)
	}
}
