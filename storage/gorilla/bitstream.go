package gorilla

import (
	"fmt"
)

// BitStream provides bit-level read/write operations
type BitStream struct {
	data    []byte
	bitPos  int // Current bit position for writing
	readPos int // Current bit position for reading
}

// NewBitStream creates a new bit stream for writing
func NewBitStream() *BitStream {
	return &BitStream{
		data: make([]byte, 0, 1024),
	}
}

// NewBitStreamFromBytes creates a bit stream for reading from existing data
func NewBitStreamFromBytes(data []byte) *BitStream {
	return &BitStream{
		data:    data,
		readPos: 0,
	}
}

// WriteBit writes a single bit
func (bs *BitStream) WriteBit(bit uint8) {
	bytePos := bs.bitPos / 8
	bitOffset := bs.bitPos % 8

	// Expand data if needed
	for len(bs.data) <= bytePos {
		bs.data = append(bs.data, 0)
	}

	if bit != 0 {
		bs.data[bytePos] |= 1 << (7 - bitOffset)
	}

	bs.bitPos++
}

// WriteBits writes multiple bits (up to 64)
func (bs *BitStream) WriteBits(value uint64, numBits int) {
	for i := numBits - 1; i >= 0; i-- {
		bit := uint8((value >> i) & 1)
		bs.WriteBit(bit)
	}
}

// ReadBit reads a single bit
func (bs *BitStream) ReadBit() (uint8, error) {
	bytePos := bs.readPos / 8
	bitOffset := bs.readPos % 8

	if bytePos >= len(bs.data) {
		return 0, fmt.Errorf("end of stream")
	}

	bit := (bs.data[bytePos] >> (7 - bitOffset)) & 1
	bs.readPos++

	return bit, nil
}

// ReadBits reads multiple bits (up to 64)
func (bs *BitStream) ReadBits(numBits int) (uint64, error) {
	var value uint64

	for i := 0; i < numBits; i++ {
		bit, err := bs.ReadBit()
		if err != nil {
			return 0, err
		}
		value = (value << 1) | uint64(bit)
	}

	return value, nil
}

// Bytes returns the byte array
func (bs *BitStream) Bytes() []byte {
	return bs.data
}

// BitLength returns the number of bits written
func (bs *BitStream) BitLength() int {
	return bs.bitPos
}
