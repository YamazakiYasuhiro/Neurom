package bus

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
)

// TCPBridgeConfig holds configuration for the TCP bridge.
type TCPBridgeConfig struct {
	Bus         *ChannelBus
	TCPEndpoint string
	MaxRestarts int
}

// TCPBridge bridges messages between a ChannelBus and a TCP endpoint.
// It runs in a separate goroutine with panic recovery, isolating
// zmq4 TCP transport failures from the rest of the application.
type TCPBridge struct {
	config   TCPBridgeConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
	restarts int

	// testPanicOnRecv triggers a panic on the next Recv for testing.
	testPanicOnRecv bool
	// testAlwaysPanic makes run() panic immediately every time (for max-restart testing).
	testAlwaysPanic bool
}

func NewTCPBridge(cfg TCPBridgeConfig) *TCPBridge {
	if cfg.MaxRestarts == 0 {
		cfg.MaxRestarts = 5
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPBridge{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (tb *TCPBridge) Start() error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.running {
		return nil
	}

	tb.running = true
	tb.wg.Add(1)
	go func() {
		defer tb.wg.Done()
		tb.runWithRecovery()
	}()

	return nil
}

func (tb *TCPBridge) Close() error {
	tb.cancel()
	tb.wg.Wait()
	return nil
}

func (tb *TCPBridge) runWithRecovery() {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[TCPBridge] recovered from panic: %v", r)
					tb.mu.Lock()
					tb.restarts++
					tb.mu.Unlock()
				}
			}()
			tb.run()
		}()

		if tb.ctx.Err() != nil {
			return
		}

		tb.mu.Lock()
		restarts := tb.restarts
		tb.mu.Unlock()

		if restarts >= tb.config.MaxRestarts {
			log.Printf("[TCPBridge] max restarts (%d) exceeded, giving up", tb.config.MaxRestarts)
			return
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func (tb *TCPBridge) run() {
	if tb.testAlwaysPanic {
		panic("test: always panic")
	}

	pubCtx, pubCancel := context.WithCancel(tb.ctx)
	defer pubCancel()

	pub := zmq4.NewPub(pubCtx)
	if err := pub.Listen(tb.config.TCPEndpoint); err != nil {
		log.Printf("[TCPBridge] failed to listen on %s: %v", tb.config.TCPEndpoint, err)
		return
	}
	defer pub.Close()

	ch, err := tb.config.Bus.Subscribe("")
	if err != nil {
		log.Printf("[TCPBridge] failed to subscribe to bus: %v", err)
		return
	}

	for {
		select {
		case <-tb.ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}

			if tb.testPanicOnRecv {
				tb.testPanicOnRecv = false
				panic("test panic in TCPBridge")
			}

			data, err := msg.Encode()
			if err != nil {
				continue
			}

			topic := msg.Target
			zm := zmq4.NewMsgFrom([]byte(topic), data)
			if err := pub.Send(zm); err != nil {
				log.Printf("[TCPBridge] send error: %v", err)
				return
			}
		}
	}
}
