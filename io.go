package piper

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
	_, buf, err := pio.pipe.conn.ReadMessage()
	msg, err := DecodeMessage(buf)
	if err != nil {
		return 0, err
	}
	c := 0
	if msg.Kind == pio.kind {
		for i := range msg.Payload {
			c += 1
			b[i] = msg.Payload[i]
		}
	}
	return c, nil
}
