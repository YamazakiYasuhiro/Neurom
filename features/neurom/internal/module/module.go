package module

import (
	"context"

	"github.com/axsh/neurom/internal/bus"
)

// Module represents a hardware component that connects to the VM's message bus.
type Module interface {
	Name() string
	Start(ctx context.Context, b bus.Bus) error
	Stop() error
}
