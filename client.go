package piper

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func NewClientPipe(host string, opts Opts, logger *log.Logger) (*Pipe, error) {
	encoded, err := json.Marshal(opts)
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Add("X-Pipe-Opts", string(encoded))
	url := fmt.Sprintf("ws://%s", host)
	conn, _, err := websocket.DefaultDialer.Dial(url, h)
	if err != nil {
		return nil, err
	}
	pipe := NewPipe(conn, opts, logger)
	return pipe, nil
}

func (pipe *Pipe) Interact() (int, error) {
	go func() {
		stdinPipe := pipeIO{pipe: pipe, kind: STDIN}
		_, err := io.Copy(stdinPipe, os.Stdin)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		pipe.sendEOF()
	}()
	var exitCode int
	pipe.conn.SetReadDeadline(time.Now().Add(pongWait))
	pipe.conn.SetPongHandler(func(string) error {
		pipe.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
loop:
	for {
		_, r, err := pipe.conn.NextReader()
		if err != nil {
			if err == io.EOF {
				return 1, fmt.Errorf("received EOF before exit code")
			}
			return 1, fmt.Errorf("reading message: %s", err)
		}
		m, err := DecodeMessage(r)
		if err != nil {
			return 1, fmt.Errorf("decoding message: %s", err)
		}
		switch m.Kind {
		case STDOUT:
			os.Stdout.Write(m.Payload)
			break
		case STDERR:
			os.Stderr.Write(m.Payload)
			break
		case EXIT:
			exitCode = int(m.ExitCode)
			break loop
		}
	}
	pipe.Close("")
	return exitCode, nil
}
