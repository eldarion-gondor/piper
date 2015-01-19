package piperc

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"

	"github.com/gorilla/websocket"
)

type Pipe struct {
	conn  *websocket.Conn
	ready chan struct{}
}

func NewPipe(host, key string) (*Pipe, error) {
	url := fmt.Sprintf("ws://%s/%s", host, key)
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		return nil, err
	}
	pipe := &Pipe{
		conn:  conn,
		ready: make(chan struct{}, 1),
	}
	go func() {
		for {
			_, p, err := pipe.conn.ReadMessage()
			if err != nil {
				break
			}
			m, _ := DecodeMessage(p)
			switch m.Kind {
			case READY:
				pipe.ready <- struct{}{}
				break
			}
		}
	}()
	return pipe, nil
}

func (pipe *Pipe) Wait() {
	<-pipe.ready
}

func (pipe *Pipe) SendOutput(kind int, msg []byte) {
	sendOutput(pipe.conn, kind, msg)
}

func (pipe *Pipe) SendExit(code uint32) {
	sendExit(pipe.conn, code)
}

func (pipe *Pipe) SendError(msg string) {
	sendOutput(pipe.conn, STDERR, []byte(fmt.Sprintf("Error: %s\n", msg)))
	sendExit(pipe.conn, 1)
	pipe.Close("")
}

func (pipe *Pipe) RunCmd(cmd *exec.Cmd) (uint32, error) {
	errc := make(chan error, 1)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return uint32(1), err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return uint32(1), err
	}
	pipe.CopyStreamAsync(errc, stdout, STDOUT)
	pipe.CopyStreamAsync(errc, stderr, STDERR)
	if err := cmd.Start(); err != nil {
		return uint32(1), err
	}
	if err := <-errc; err != nil {
		return uint32(1), err
	}
	status, err := exitStatus(cmd.Wait())
	if err != nil {
		return uint32(1), err
	}
	return status, nil
}

func exitStatus(err error) (uint32, error) {
	if err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				return uint32(status.ExitStatus()), nil
			}
		}
		return 0, err
	}
	return 0, nil
}

func (pipe *Pipe) CopyStreamAsync(errc chan error, stream io.Reader, outputStream int) {
	go func() {
		err := pipe.CopyStream(stream, outputStream)
		if errc != nil {
			errc <- err
		}
	}()
}

func (pipe *Pipe) CopyStream(stream io.Reader, outputStream int) error {
	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		sendOutput(pipe.conn, outputStream, append(scanner.Bytes(), '\n'))
	}
	err := scanner.Err()
	return err
}

func (pipe *Pipe) Watch() error {
	var exitCode int
loop:
	for {
		_, p, err := pipe.conn.ReadMessage()
		if err != nil {
			return err
		}
		m, err := DecodeMessage(p)
		if err != nil {
			return err
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
	os.Exit(exitCode)
	return nil
}

func (pipe *Pipe) Close(msg string) {
	pipe.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
	)
	pipe.conn.Close()
}
