package piper

import (
	"bytes"
	"encoding/gob"
	"io"
)

const (
	READY = iota
	EXIT
	STDOUT
	STDERR
	STDIN
	EOF
)

type Message struct {
	Kind     int
	Payload  []byte
	ExitCode uint32
}

func DecodeMessage(r io.Reader) (*Message, error) {
	m := new(Message)
	dec := gob.NewDecoder(r)
	err := dec.Decode(m)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Message) Prepare() ([]byte, error) {
	mbuf := new(bytes.Buffer)
	enc := gob.NewEncoder(mbuf)
	err := enc.Encode(&m)
	if err != nil {
		return nil, err
	}
	return mbuf.Bytes(), nil
}
