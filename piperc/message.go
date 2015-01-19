package piperc

import (
	"bytes"
	"encoding/gob"

	"github.com/gorilla/websocket"
)

const (
	READY = iota
	EXIT
	STDOUT
	STDERR
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

func sendOutput(conn *websocket.Conn, kind int, payload []byte) error {
	m := &Message{
		Kind:    kind,
		Payload: payload,
	}
	return m.Send(conn)
}

func sendExit(conn *websocket.Conn, code uint32) error {
	m := &Message{
		Kind:     EXIT,
		ExitCode: code,
	}
	return m.Send(conn)
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

func (m *Message) Send(conn *websocket.Conn) error {
	txtMsg, err := m.Prepare()
	if err != nil {
		return err
	}
	err = conn.WriteMessage(websocket.BinaryMessage, txtMsg)
	if err != nil {
		return err
	}
	return nil
}
