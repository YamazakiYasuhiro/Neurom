package bus

// Bus defines the interface for the message bus.
type Bus interface {
	Publish(topic string, msg *BusMessage) error
	Subscribe(topic string) (<-chan *BusMessage, error)
	Close() error
}
