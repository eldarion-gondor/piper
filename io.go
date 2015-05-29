package piper

import "io"

type pipeIO struct {
	pipe *Pipe
	kind int
}

func (pio pipeIO) Write(b []byte) (int, error) {
	msg := &Message{
		Kind:    pio.kind,
		Payload: b,
	}
	payload, err := msg.Prepare()
	if err != nil {
		return 0, err
	}
	pio.pipe.send <- payload
	return len(b), nil
}

func (pio pipeIO) Read(b []byte) (int, error) {
	_, r, err := pio.pipe.conn.NextReader()
	if err != nil {
		return 0, err
	}
	msg, err := DecodeMessage(r)
	if err != nil {
		return 0, err
	}
	c := 0
	switch msg.Kind {
	case pio.kind:
		for i := range msg.Payload {
			c += 1
			b[i] = msg.Payload[i]
		}
	case EOF:
		return 0, io.EOF
	}
	return c, nil
}
