package piper

import (
	"bytes"
	"encoding/gob"
)

const (
	READY = iota
	EXIT
	STDOUT
	STDERR
	STDIN
)

type Message struct {
	Kind     int
	Payload  []byte
	ExitCode uint32
}

func DecodeMessage(buf []byte) (*Message, error) {
	mbuf := bytes.NewBuffer(buf)
	m := new(Message)
	dec := gob.NewDecoder(mbuf)
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
