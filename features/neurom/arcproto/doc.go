// Package arcproto defines the wire protocol of the ARC message bus.
//
// It is the single source of truth for the byte layout of every bus command and
// event. Both sides of the bus depend on it: the platform modules under
// internal/modules decode with it, and extension software encodes with it.
// Keeping one symmetric definition per message removes the duplicated offset
// arithmetic that previously lived in the producer and the consumer separately.
//
// The package deliberately sits outside internal/ so that extension software in
// other Go modules can import it.
//
// # Byte order
//
// All multi-byte integers are big-endian. Signed fields use two's complement.
//
// # Encoding and decoding
//
// Every message type provides Target and Encode; every message type with a
// payload has a matching Decode function:
//
//	data := arcproto.DrawPixel{Page: 0, X: 10, Y: 20, P: 7}.Encode()
//	cmd, err := arcproto.DecodeDrawPixel(data)
//
// Decoders reject payloads shorter than the fixed part of their layout with
// ErrShortPayload and ignore trailing bytes, which lets the protocol grow
// without breaking existing receivers. Variable-length pixel and colour data is
// returned as a subslice of the input buffer rather than a copy, so callers must
// not retain it after the underlying bus message is released.
package arcproto
