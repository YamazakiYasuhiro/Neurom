package io

import (
	"context"

	"github.com/axsh/neurom/internal/bus"
)

type IOModule struct{}

func New() *IOModule {
	return &IOModule{}
}

func (i *IOModule) Name() string {
	return "IO"
}

func (i *IOModule) Start(ctx context.Context, b bus.Bus) error {
	go func() {
		<-ctx.Done()
	}()
	return nil
}

func (i *IOModule) Stop() error {
	return nil
}
