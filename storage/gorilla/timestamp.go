package gorilla

// TimestampEncoder compresses timestamp deltas using Gorilla algorithm
type TimestampEncoder struct {
	stream       *BitStream
	prevDelta    int64
	initialized  bool
}

// NewTimestampEncoder creates a new timestamp encoder
func NewTimestampEncoder() *TimestampEncoder {
	return &TimestampEncoder{
		stream: NewBitStream(),
	}
}

// Encode compresses a timestamp delta
func (e *TimestampEncoder) Encode(delta int64) {
	if !e.initialized {
		// First delta: store as 14 bits (supports deltas up to 16383)
		e.stream.WriteBits(uint64(delta), 14)
		e.prevDelta = delta
		e.initialized = true
		return
	}

	deltaOfDelta := delta - e.prevDelta

	if deltaOfDelta == 0 {
		// Delta of delta is zero: write single '0' bit
		e.stream.WriteBit(0)
	} else if deltaOfDelta >= -63 && deltaOfDelta <= 64 {
		// Delta of delta fits in 7 bits: write '10' + 7 bits
		e.stream.WriteBit(1)
		e.stream.WriteBit(0)
		e.stream.WriteBits(uint64(deltaOfDelta), 7)
	} else if deltaOfDelta >= -255 && deltaOfDelta <= 256 {
		// Delta of delta fits in 9 bits: write '110' + 9 bits
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBit(0)
		e.stream.WriteBits(uint64(deltaOfDelta), 9)
	} else if deltaOfDelta >= -2047 && deltaOfDelta <= 2048 {
		// Delta of delta fits in 12 bits: write '1110' + 12 bits
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBit(0)
		e.stream.WriteBits(uint64(deltaOfDelta), 12)
	} else {
		// Delta of delta requires 32 bits: write '1111' + 32 bits
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBit(1)
		e.stream.WriteBits(uint64(deltaOfDelta), 32)
	}

	e.prevDelta = delta
}

// Bytes returns the compressed byte stream
func (e *TimestampEncoder) Bytes() []byte {
	return e.stream.Bytes()
}

// TimestampDecoder decompresses timestamp deltas
type TimestampDecoder struct {
	stream      *BitStream
	prevDelta   int64
	initialized bool
}

// NewTimestampDecoder creates a new timestamp decoder
func NewTimestampDecoder(data []byte) *TimestampDecoder {
	return &TimestampDecoder{
		stream: NewBitStreamFromBytes(data),
	}
}

// Decode decompresses a timestamp delta
func (d *TimestampDecoder) Decode() (int64, error) {
	if !d.initialized {
		// First delta: read 14 bits
		bits, err := d.stream.ReadBits(14)
		if err != nil {
			return 0, err
		}
		delta := int64(bits)
		d.prevDelta = delta
		d.initialized = true
		return delta, nil
	}

	// Read control bits
	bit, err := d.stream.ReadBit()
	if err != nil {
		return 0, err
	}

	var deltaOfDelta int64

	if bit == 0 {
		// Delta of delta is zero
		deltaOfDelta = 0
	} else {
		bit, err = d.stream.ReadBit()
		if err != nil {
			return 0, err
		}

		if bit == 0 {
			// Read 7 bits
			bits, err := d.stream.ReadBits(7)
			if err != nil {
				return 0, err
			}
			deltaOfDelta = signExtend(bits, 7)
		} else {
			bit, err = d.stream.ReadBit()
			if err != nil {
				return 0, err
			}

			if bit == 0 {
				// Read 9 bits
				bits, err := d.stream.ReadBits(9)
				if err != nil {
					return 0, err
				}
				deltaOfDelta = signExtend(bits, 9)
			} else {
				bit, err = d.stream.ReadBit()
				if err != nil {
					return 0, err
				}

				if bit == 0 {
					// Read 12 bits
					bits, err := d.stream.ReadBits(12)
					if err != nil {
						return 0, err
					}
					deltaOfDelta = signExtend(bits, 12)
				} else {
					// Read 32 bits
					bits, err := d.stream.ReadBits(32)
					if err != nil {
						return 0, err
					}
					deltaOfDelta = signExtend(bits, 32)
				}
			}
		}
	}

	delta := d.prevDelta + deltaOfDelta
	d.prevDelta = delta
	return delta, nil
}

// signExtend extends the sign of a value stored in n bits
func signExtend(value uint64, bits int) int64 {
	// Check if the sign bit is set
	signBit := uint64(1) << (bits - 1)
	if value&signBit != 0 {
		// Extend with 1s
		mask := ^uint64(0) << bits
		return int64(value | mask)
	}
	return int64(value)
}
