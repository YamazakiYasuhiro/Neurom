package bus

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

const (
	ChunkMagic     byte = 0xC1
	ChunkHeaderLen      = 17
	ChunkThreshold      = 32 * 1024 // 32 KB
)

type chunkHeader struct {
	MessageID  uint64
	ChunkIndex uint16
	TotalCount uint16
	TotalSize  uint32
}

func encodeChunkHeader(h chunkHeader) []byte {
	buf := make([]byte, ChunkHeaderLen)
	buf[0] = ChunkMagic
	binary.BigEndian.PutUint64(buf[1:9], h.MessageID)
	binary.BigEndian.PutUint16(buf[9:11], h.ChunkIndex)
	binary.BigEndian.PutUint16(buf[11:13], h.TotalCount)
	binary.BigEndian.PutUint32(buf[13:17], h.TotalSize)
	return buf
}

func decodeChunkHeader(data []byte) (chunkHeader, error) {
	if len(data) < ChunkHeaderLen {
		return chunkHeader{}, fmt.Errorf("chunk frame too short: %d bytes", len(data))
	}
	if data[0] != ChunkMagic {
		return chunkHeader{}, fmt.Errorf("invalid chunk magic: 0x%02X", data[0])
	}
	return chunkHeader{
		MessageID:  binary.BigEndian.Uint64(data[1:9]),
		ChunkIndex: binary.BigEndian.Uint16(data[9:11]),
		TotalCount: binary.BigEndian.Uint16(data[11:13]),
		TotalSize:  binary.BigEndian.Uint32(data[13:17]),
	}, nil
}

// IsChunkedFrame returns true if data starts with ChunkMagic.
func IsChunkedFrame(data []byte) bool {
	return len(data) > 0 && data[0] == ChunkMagic
}

// EncodeChunks splits payload into chunks if it exceeds ChunkThreshold.
// Returns nil if no chunking is needed.
func EncodeChunks(msgID uint64, payload []byte) [][]byte {
	if len(payload) <= ChunkThreshold {
		return nil
	}

	totalSize := len(payload)
	totalCount := (totalSize + ChunkThreshold - 1) / ChunkThreshold
	chunks := make([][]byte, totalCount)

	for i := range totalCount {
		start := i * ChunkThreshold
		end := start + ChunkThreshold
		if end > totalSize {
			end = totalSize
		}
		slice := payload[start:end]

		hdr := encodeChunkHeader(chunkHeader{
			MessageID:  msgID,
			ChunkIndex: uint16(i),
			TotalCount: uint16(totalCount),
			TotalSize:  uint32(totalSize),
		})

		frame := make([]byte, ChunkHeaderLen+len(slice))
		copy(frame, hdr)
		copy(frame[ChunkHeaderLen:], slice)
		chunks[i] = frame
	}

	return chunks
}

type reassemblyEntry struct {
	chunks    [][]byte
	received  int
	total     int
	totalSize int
	createdAt time.Time
}

// ChunkReassembler manages reassembly of chunked messages.
type ChunkReassembler struct {
	mu      sync.Mutex
	pending map[uint64]*reassemblyEntry
	timeout time.Duration
}

func NewChunkReassembler(timeout time.Duration) *ChunkReassembler {
	return &ChunkReassembler{
		pending: make(map[uint64]*reassemblyEntry),
		timeout: timeout,
	}
}

// Add processes a single chunk frame.
// Returns (assembled payload, true) when all chunks are received.
func (r *ChunkReassembler) Add(data []byte) ([]byte, bool) {
	hdr, err := decodeChunkHeader(data)
	if err != nil {
		return nil, false
	}

	payload := data[ChunkHeaderLen:]

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.pending[hdr.MessageID]
	if !exists {
		entry = &reassemblyEntry{
			chunks:    make([][]byte, hdr.TotalCount),
			total:     int(hdr.TotalCount),
			totalSize: int(hdr.TotalSize),
			createdAt: time.Now(),
		}
		r.pending[hdr.MessageID] = entry
	}

	idx := int(hdr.ChunkIndex)
	if idx >= entry.total {
		return nil, false
	}

	if entry.chunks[idx] == nil {
		entry.received++
	}
	entry.chunks[idx] = payload

	if entry.received < entry.total {
		return nil, false
	}

	assembled := make([]byte, 0, entry.totalSize)
	for _, chunk := range entry.chunks {
		assembled = append(assembled, chunk...)
	}
	delete(r.pending, hdr.MessageID)

	return assembled, true
}

// CleanExpired removes entries older than timeout.
func (r *ChunkReassembler) CleanExpired() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	for id, entry := range r.pending {
		if now.Sub(entry.createdAt) > r.timeout {
			delete(r.pending, id)
		}
	}
}
