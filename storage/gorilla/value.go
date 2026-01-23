package gorilla

import (
	"math"
	"math/bits"
)

// ValueEncoder compresses float64 values using XOR encoding
type ValueEncoder struct {
	stream           *BitStream
	prevLeadingZeros int
	prevTrailingZeros int
}

// NewValueEncoder creates a new value encoder
func NewValueEncoder() *ValueEncoder {
	return &ValueEncoder{
		stream:           NewBitStream(),
		prevLeadingZeros: 0,
		prevTrailingZeros: 0,
	}
}

// Encode compresses a value using XOR with previous value
func (e *ValueEncoder) Encode(value float64, prevValue float64) {
	// Convert to uint64 for bit manipulation
	valueBits := math.Float64bits(value)
	prevBits := math.Float64bits(prevValue)

	// XOR with previous value
	xor := valueBits ^ prevBits

	if xor == 0 {
		// Value is identical: write single '0' bit
		e.stream.WriteBit(0)
		return
	}

	// Value is different: write '1' bit
	e.stream.WriteBit(1)

	// Count leading and trailing zeros
	leadingZeros := bits.LeadingZeros64(xor)
	trailingZeros := bits.TrailingZeros64(xor)

	// If xor is all zeros (shouldn't happen due to check above), handle edge case
	if leadingZeros == 64 {
		leadingZeros = 0
		trailingZeros = 0
	}

	// Check if we can use the previous block size
	if leadingZeros >= e.prevLeadingZeros && trailingZeros >= e.prevTrailingZeros && e.prevLeadingZeros > 0 {
		// Control bit '0': use previous block
		e.stream.WriteBit(0)

		// Write the meaningful bits
		significantBits := 64 - e.prevLeadingZeros - e.prevTrailingZeros
		xorShifted := xor >> e.prevTrailingZeros
		e.stream.WriteBits(xorShifted, significantBits)
	} else {
		// Control bit '1': new block
		e.stream.WriteBit(1)

		// Write leading zeros (5 bits can represent 0-31)
		if leadingZeros > 31 {
			leadingZeros = 31
		}
		e.stream.WriteBits(uint64(leadingZeros), 5)

		// Calculate significant bits length
		significantBits := 64 - leadingZeros - trailingZeros
		if significantBits > 64 {
			significantBits = 64
		}
		if significantBits == 0 {
			significantBits = 1
		}

		// Write significant bits length (6 bits can represent 1-64)
		e.stream.WriteBits(uint64(significantBits-1), 6)

		// Write the significant bits
		xorShifted := xor >> trailingZeros
		e.stream.WriteBits(xorShifted, significantBits)

		// Update previous block size
		e.prevLeadingZeros = leadingZeros
		e.prevTrailingZeros = trailingZeros
	}
}

// Bytes returns the compressed byte stream
func (e *ValueEncoder) Bytes() []byte {
	return e.stream.Bytes()
}

// ValueDecoder decompresses float64 values
type ValueDecoder struct {
	stream            *BitStream
	prevLeadingZeros  int
	prevTrailingZeros int
}

// NewValueDecoder creates a new value decoder
func NewValueDecoder(data []byte) *ValueDecoder {
	return &ValueDecoder{
		stream:            NewBitStreamFromBytes(data),
		prevLeadingZeros:  0,
		prevTrailingZeros: 0,
	}
}

// Decode decompresses a value
func (d *ValueDecoder) Decode(prevValue float64) (float64, error) {
	// Read control bit
	bit, err := d.stream.ReadBit()
	if err != nil {
		return 0, err
	}

	if bit == 0 {
		// Value is identical to previous
		return prevValue, nil
	}

	// Value is different: read next control bit
	bit, err = d.stream.ReadBit()
	if err != nil {
		return 0, err
	}

	var xor uint64

	if bit == 0 {
		// Use previous block size
		significantBits := 64 - d.prevLeadingZeros - d.prevTrailingZeros
		bits, err := d.stream.ReadBits(significantBits)
		if err != nil {
			return 0, err
		}
		xor = bits << d.prevTrailingZeros
	} else {
		// New block: read leading zeros (5 bits)
		leadingBits, err := d.stream.ReadBits(5)
		if err != nil {
			return 0, err
		}
		leadingZeros := int(leadingBits)

		// Read significant bits length (6 bits)
		significantLenBits, err := d.stream.ReadBits(6)
		if err != nil {
			return 0, err
		}
		significantBits := int(significantLenBits) + 1

		// Read significant bits
		bits, err := d.stream.ReadBits(significantBits)
		if err != nil {
			return 0, err
		}

		trailingZeros := 64 - leadingZeros - significantBits
		xor = bits << trailingZeros

		// Update previous block size
		d.prevLeadingZeros = leadingZeros
		d.prevTrailingZeros = trailingZeros
	}

	// XOR with previous value
	prevBits := math.Float64bits(prevValue)
	valueBits := prevBits ^ xor

	return math.Float64frombits(valueBits), nil
}
