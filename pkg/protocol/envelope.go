package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Envelope is the first half of the two-step decode: read "type", then
// unmarshal the same bytes into the concrete struct for that type.
type Envelope struct {
	Type string `json:"type"`
}

func PeekType(line []byte) (string, error) {
	var e Envelope
	if err := json.Unmarshal(line, &e); err != nil {
		return "", fmt.Errorf("decode envelope: %w", err)
	}
	if e.Type == "" {
		return "", errors.New("decode envelope: missing type")
	}
	return e.Type, nil
}

// EncodeLine marshals v and appends the "\n" that frames TCP messages.
func EncodeLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
