package bus

import (
	"bytes"
	"encoding/gob"
	"fmt"
)

// OpType represents the type of operation in a BusMessage.
type OpType uint8

const (
	OpWrite OpType = iota
	OpRead
	OpCommand
)

// System-level command constants.
const (
	TargetSystem = "System"
	CmdShutdown  = "Shutdown"
)

// BusMessage is the message structure that flows through the system bus.
type BusMessage struct {
	Target    string
	Operation OpType
	Data      []byte
	Source    string
}

// Encode serializes the BusMessage into a byte slice.
func (m *BusMessage) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("serialize message failed: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode deserializes a byte slice into a BusMessage.
func Decode(data []byte) (*BusMessage, error) {
	var msg BusMessage
	buf := bytes.NewReader(data)
	dec := gob.NewDecoder(buf)
	if err := dec.Decode(&msg); err != nil {
		return nil, fmt.Errorf("deserialize message failed: %w", err)
	}
	return &msg, nil
}
