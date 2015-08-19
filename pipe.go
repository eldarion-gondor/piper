package piper

import (
	"io"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// send pings to peer with this period. must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
)

type Pipe struct {
	conn *websocket.Conn
	send chan []byte
	opts Opts
}

type Opts struct {
	Tty    bool `json:"tty"`
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
}

type Winsize struct {
	Height uint16
	Width  uint16
	x      uint16
	y      uint16
}

func (pipe *Pipe) writer() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		pipe.conn.Close()
	}()
	write := func(mt int, payload []byte) error {
		pipe.conn.SetWriteDeadline(time.Now().Add(writeWait))
		return pipe.conn.WriteMessage(mt, payload)
	}
	for {
		select {
		case msg, ok := <-pipe.send:
			if !ok {
				write(websocket.CloseMessage, []byte{})
				return
			}
			if err := write(websocket.BinaryMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			if err := write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (pipe *Pipe) sendExit(code uint32) {
	msg := Message{
		Kind:     EXIT,
		ExitCode: code,
	}
	payload, _ := msg.Prepare()
	pipe.send <- payload
}

func (pipe *Pipe) sendEOF() {
	msg := Message{Kind: EOF}
	payload, _ := msg.Prepare()
	pipe.send <- payload
}

func (pipe *Pipe) writeTo(w io.WriteCloser, kind int) error {
	defer w.Close()
	_, err := io.Copy(w, pipeIO{pipe: pipe, kind: kind})
	if err != nil {
		return err
	}
	return nil
}

func (pipe *Pipe) readFrom(r io.Reader, kind int) error {
	_, err := io.Copy(pipeIO{pipe: pipe, kind: kind}, r)
	if err != nil {
		return err
	}
	return nil
}

func (pipe *Pipe) Close(msg string) {
	pipe.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
	)
	pipe.conn.Close()
}
