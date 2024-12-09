package embedding

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// todo move to vector.go
type EmbeddingVector []float32

func (ev EmbeddingVector) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	err := binary.Write(&buf, binary.LittleEndian, ev)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (ev *EmbeddingVector) UnmarshalBinary(data []byte) error {
	// Determine the number of float32 elements in the data
	if len(data)%4 != 0 {
		return errors.New("data length is not a multiple of 4")
	}
	numElements := len(data) / 4

	// Make sure ev has the correct length and capacity
	*ev = make(EmbeddingVector, numElements)

	// Now unmarshal
	buf := bytes.NewBuffer(data)
	err := binary.Read(buf, binary.LittleEndian, ev)
	if err != nil {
		return err
	}
	return nil
}
