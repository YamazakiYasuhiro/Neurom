package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-zeromq/zmq4"
)

type ZMQBus struct {
	pubCtx    context.Context
	pubCancel context.CancelFunc
	pubSocket zmq4.Socket

	subCtx    context.Context
	subCancel context.CancelFunc
	subSocket zmq4.Socket

	endpoints []string

	mu   sync.Mutex
	subs map[string][]chan *BusMessage
}

// NewZMQBus creates a new ZMQ-based message bus.
// It tries to bind/connect to the given endpoints.
func NewZMQBus(endpoints ...string) (*ZMQBus, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints provided")
	}

	pubCtx, pubCancel := context.WithCancel(context.Background())
	pub := zmq4.NewPub(pubCtx)

	for _, ep := range endpoints {
		if err := pub.Listen(ep); err != nil {
			pubCancel()
			pub.Close()
			return nil, fmt.Errorf("failed to listen on %s: %w", ep, err)
		}
	}

	// For subscriber inside the bus, we use a single SUB socket and connect to the PUB socket(s).
	subCtx, subCancel := context.WithCancel(context.Background())
	sub := zmq4.NewSub(subCtx)

	for _, ep := range endpoints {
		if err := sub.Dial(ep); err != nil {
			subCancel()
			sub.Close()
			pubCancel()
			pub.Close()
			return nil, fmt.Errorf("failed to dial on %s: %w", ep, err)
		}
	}

	// Give ZMQ a short time to establish pub/sub connections
	time.Sleep(100 * time.Millisecond)

	b := &ZMQBus{
		pubCtx:    pubCtx,
		pubCancel: pubCancel,
		pubSocket: pub,
		subCtx:    subCtx,
		subCancel: subCancel,
		subSocket: sub,
		endpoints: endpoints,
		subs:      make(map[string][]chan *BusMessage),
	}

	go b.receiveLoop()

	return b, nil
}

func (b *ZMQBus) Publish(topic string, msg *BusMessage) error {
	data, err := msg.Encode()
	if err != nil {
		return err
	}
	
	zm := zmq4.NewMsgFrom([]byte(topic), data)
	return b.pubSocket.Send(zm)
}

func (b *ZMQBus) Subscribe(topic string) (<-chan *BusMessage, error) {
	// Let the ZMQ socket subscribe to the topic
	if err := b.subSocket.SetOption(zmq4.OptionSubscribe, topic); err != nil {
		return nil, fmt.Errorf("zmq subscribe option failed: %w", err)
	}

	ch := make(chan *BusMessage, 100)

	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], ch)
	b.mu.Unlock()

	return ch, nil
}

func (b *ZMQBus) receiveLoop() {
	for {
		zmsg, err := b.subSocket.Recv()
		if err != nil {
			// Sub socket closed or context canceled
			return
		}

		if len(zmsg.Frames) < 2 {
			continue
		}

		topic := string(zmsg.Frames[0])
		data := zmsg.Frames[1]

		msg, err := Decode(data)
		if err != nil {
			// Malformed message
			continue
		}

		b.mu.Lock()
		for subTopic, chans := range b.subs {
			// ZeroMQ matching logic: if the message topic starts with the subscribed string
			if strings.HasPrefix(topic, subTopic) {
				for _, ch := range chans {
					select {
					case ch <- msg:
					default:
						// Skip if channel is full
					}
				}
			}
		}
		b.mu.Unlock()
	}
}

func (b *ZMQBus) Close() error {
	b.subCancel()
	b.pubCancel()
	
	err1 := b.subSocket.Close()
	err2 := b.pubSocket.Close()

	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}
