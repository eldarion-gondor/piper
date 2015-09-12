package piper

import (
	"io"
	"log"
	"sync"
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
	send chan *syncPayload
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

type syncPayload struct {
	buf []byte
	wg  sync.WaitGroup
}

func NewPipe(conn *websocket.Conn, opts Opts) *Pipe {
	pipe := &Pipe{
		conn: conn,
		send: make(chan *syncPayload),
		opts: opts,
	}
	go pipe.writer()
	return pipe
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
		case sp, ok := <-pipe.send:
			if !ok {
				log.Println("pipe: writing close message")
				write(websocket.CloseMessage, []byte{})
				return
			}
			if err := write(websocket.BinaryMessage, sp.buf); err != nil {
				sp.wg.Done()
				return
			}
			sp.wg.Done()
		case <-ticker.C:
			log.Println("pipe: writing ping message")
			if err := write(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (pipe *Pipe) Send(payload []byte) {
	sp := &syncPayload{buf: payload}
	sp.wg.Add(1)
	pipe.send <- sp
	sp.wg.Wait()
}

func (pipe *Pipe) sendExit(code uint32) {
	msg := Message{
		Kind:     EXIT,
		ExitCode: code,
	}
	payload, _ := msg.Prepare()
	log.Printf("pipe: sending EXIT %#v", payload)
	pipe.Send(payload)
}

func (pipe *Pipe) sendEOF() {
	msg := Message{Kind: EOF}
	payload, _ := msg.Prepare()
	log.Printf("pipe: sending EOF %#v", payload)
	pipe.Send(payload)
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
	log.Printf("pipe: closing (msg=%q)", msg)
	pipe.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
	)
	pipe.conn.Close()
}
