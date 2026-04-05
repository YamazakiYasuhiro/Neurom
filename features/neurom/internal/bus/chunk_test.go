package bus

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestEncodeChunks_SmallPayload(t *testing.T) {
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte(i)
	}

	chunks := EncodeChunks(1, payload)
	if chunks != nil {
		t.Errorf("expected nil for small payload, got %d chunks", len(chunks))
	}
}

func TestEncodeChunks_ExactThreshold(t *testing.T) {
	payload := make([]byte, ChunkThreshold)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	chunks := EncodeChunks(1, payload)
	if chunks != nil {
		t.Errorf("expected nil for exact-threshold payload, got %d chunks", len(chunks))
	}
}

func TestEncodeChunks_LargePayload(t *testing.T) {
	payloadSize := 100 * 1024 // 100 KB
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	chunks := EncodeChunks(42, payload)
	if chunks == nil {
		t.Fatal("expected chunks for large payload, got nil")
	}

	expectedCount := (payloadSize + ChunkThreshold - 1) / ChunkThreshold // ceil division
	if len(chunks) != expectedCount {
		t.Fatalf("expected %d chunks, got %d", expectedCount, len(chunks))
	}

	var assembled []byte
	for i, chunk := range chunks {
		if len(chunk) < ChunkHeaderLen {
			t.Fatalf("chunk %d too short: %d bytes", i, len(chunk))
		}

		if chunk[0] != ChunkMagic {
			t.Errorf("chunk %d: expected magic 0x%02X, got 0x%02X", i, ChunkMagic, chunk[0])
		}

		msgID := binary.BigEndian.Uint64(chunk[1:9])
		if msgID != 42 {
			t.Errorf("chunk %d: expected msgID 42, got %d", i, msgID)
		}

		idx := binary.BigEndian.Uint16(chunk[9:11])
		if int(idx) != i {
			t.Errorf("chunk %d: expected index %d, got %d", i, i, idx)
		}

		total := binary.BigEndian.Uint16(chunk[11:13])
		if int(total) != expectedCount {
			t.Errorf("chunk %d: expected total %d, got %d", i, expectedCount, total)
		}

		totalSize := binary.BigEndian.Uint32(chunk[13:17])
		if int(totalSize) != payloadSize {
			t.Errorf("chunk %d: expected totalSize %d, got %d", i, payloadSize, totalSize)
		}

		assembled = append(assembled, chunk[ChunkHeaderLen:]...)
	}

	if !bytes.Equal(assembled, payload) {
		t.Error("reassembled payload does not match original")
	}
}

func TestEncodeChunks_EmptyPayload(t *testing.T) {
	chunks := EncodeChunks(1, nil)
	if chunks != nil {
		t.Errorf("expected nil for empty payload, got %d chunks", len(chunks))
	}

	chunks = EncodeChunks(1, []byte{})
	if chunks != nil {
		t.Errorf("expected nil for zero-length payload, got %d chunks", len(chunks))
	}
}

func TestIsChunkedFrame(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"chunk magic", []byte{ChunkMagic, 0x00}, true},
		{"gob data", []byte{0x0F, 0x00}, false},
		{"empty", []byte{}, false},
		{"nil", nil, false},
		{"single magic byte", []byte{ChunkMagic}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsChunkedFrame(tt.data)
			if got != tt.want {
				t.Errorf("IsChunkedFrame(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestChunkReassembler_Complete(t *testing.T) {
	payloadSize := 80 * 1024 // 80 KB → 3 chunks
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	chunks := EncodeChunks(7, payload)
	if chunks == nil {
		t.Fatal("expected chunks, got nil")
	}

	r := NewChunkReassembler(5 * time.Second)

	for i, chunk := range chunks {
		assembled, complete := r.Add(chunk)
		if i < len(chunks)-1 {
			if complete {
				t.Fatalf("chunk %d: expected incomplete, got complete", i)
			}
			if assembled != nil {
				t.Fatalf("chunk %d: expected nil assembled, got data", i)
			}
		} else {
			if !complete {
				t.Fatal("last chunk: expected complete, got incomplete")
			}
			if !bytes.Equal(assembled, payload) {
				t.Error("reassembled data does not match original")
			}
		}
	}
}

func TestChunkReassembler_Timeout(t *testing.T) {
	payloadSize := 80 * 1024
	payload := make([]byte, payloadSize)

	chunks := EncodeChunks(99, payload)
	if chunks == nil {
		t.Fatal("expected chunks")
	}

	r := NewChunkReassembler(10 * time.Millisecond)

	// Add only the first chunk
	_, complete := r.Add(chunks[0])
	if complete {
		t.Fatal("should not be complete with only first chunk")
	}

	time.Sleep(20 * time.Millisecond)
	r.CleanExpired()

	// Adding remaining chunks should not produce a result (entry was cleaned)
	for _, chunk := range chunks[1:] {
		_, complete = r.Add(chunk)
	}
	if complete {
		t.Error("expected incomplete after timeout cleanup, but got complete")
	}
}

func TestChunkReassembler_DuplicateChunk(t *testing.T) {
	payloadSize := 80 * 1024
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	chunks := EncodeChunks(55, payload)
	if chunks == nil {
		t.Fatal("expected chunks")
	}

	r := NewChunkReassembler(5 * time.Second)

	// Add chunk 0 twice
	r.Add(chunks[0])
	r.Add(chunks[0])

	// Add remaining chunks
	var assembled []byte
	var complete bool
	for _, chunk := range chunks[1:] {
		assembled, complete = r.Add(chunk)
	}

	if !complete {
		t.Fatal("expected complete after all chunks added")
	}
	if !bytes.Equal(assembled, payload) {
		t.Error("reassembled data does not match original after duplicate chunk")
	}
}
