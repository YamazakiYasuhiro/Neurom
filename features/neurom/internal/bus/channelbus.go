package bus

import (
	"strings"
	"sync"
)

// ChannelBus is a pure Go channel-based message bus for in-process communication.
// It avoids zmq4 entirely, eliminating the ZMTP frame parsing bugs that cause
// panics with large messages on the inproc transport.
type ChannelBus struct {
	mu     sync.Mutex
	subs   map[string][]chan *BusMessage
	closed bool
}

func NewChannelBus() *ChannelBus {
	return &ChannelBus{
		subs: make(map[string][]chan *BusMessage),
	}
}

func (b *ChannelBus) Publish(topic string, msg *BusMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	for subTopic, chans := range b.subs {
		if strings.HasPrefix(topic, subTopic) {
			for _, ch := range chans {
				select {
				case ch <- msg:
				default:
				}
			}
		}
	}
	return nil
}

func (b *ChannelBus) Subscribe(topic string) (<-chan *BusMessage, error) {
	ch := make(chan *BusMessage, 100)

	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()

	return ch, nil
}

func (b *ChannelBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	for _, chans := range b.subs {
		for _, ch := range chans {
			close(ch)
		}
	}
	b.subs = make(map[string][]chan *BusMessage)
	return nil
}
