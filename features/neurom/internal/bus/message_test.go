package bus

import (
	"bytes"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	msg := &BusMessage{
		Target:    "0x2000",
		Operation: OpWrite,
		Data:      []byte{0xDE, 0xAD, 0xBE, 0xEF},
		Source:    "CPU",
	}

	encoded, err := msg.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded.Target != msg.Target {
		t.Errorf("Expected Target %s, got %s", msg.Target, decoded.Target)
	}
	if decoded.Operation != msg.Operation {
		t.Errorf("Expected Operation %d, got %d", msg.Operation, decoded.Operation)
	}
	if !bytes.Equal(decoded.Data, msg.Data) {
		t.Errorf("Expected Data %x, got %x", msg.Data, decoded.Data)
	}
	if decoded.Source != msg.Source {
		t.Errorf("Expected Source %s, got %s", msg.Source, decoded.Source)
	}
}
