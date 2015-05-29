package piper

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kr/pty"
)

type Pipe struct {
	conn *websocket.Conn
	send chan []byte
}

type pipedCmd struct {
	pipe   *Pipe
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func NewClientPipe(host string) (*Pipe, error) {
	url := fmt.Sprintf("ws://%s", host)
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		return nil, err
	}
	pipe := &Pipe{
		conn: conn,
		send: make(chan []byte),
	}
	go pipe.writer()
	return pipe, nil
}

func NewServerPipe(conn *websocket.Conn) *Pipe {
	pipe := &Pipe{
		conn: conn,
		send: make(chan []byte),
	}
	go pipe.writer()
	return pipe
}

func (pipe *Pipe) writer() {
	ticker := time.NewTicker(((60 * time.Second) * 9) / 10)
	defer func() {
		ticker.Stop()
		pipe.conn.Close()
	}()
	write := func(mt int, payload []byte) error {
		pipe.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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

func (pipe *Pipe) RunCmd(cmd *exec.Cmd) error {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	pCmd := pipedCmd{
		pipe:   pipe,
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
	return pCmd.run()
}

func (pipe *Pipe) RunPtyCmd(cmd *exec.Cmd) error {
	py, tty, err := pty.Open()
	if err != nil {
		return err
	}
	defer tty.Close()
	cmd.Stdout = tty
	cmd.Stdin = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Setsid: true}
	pCmd := pipedCmd{
		pipe:   pipe,
		cmd:    cmd,
		stdin:  py,
		stdout: py,
		stderr: nil,
	}
	return pCmd.run()
}

func (pCmd *pipedCmd) run() error {
	errc := make(chan error, 1)
	go pCmd.pipe.copyTo(errc, pCmd.stdin, STDIN)
	go pCmd.pipe.copyFrom(errc, pCmd.stdout, STDOUT)
	if pCmd.stderr != nil {
		go pCmd.pipe.copyFrom(errc, pCmd.stderr, STDERR)
	}
	if err := pCmd.cmd.Start(); err != nil {
		pCmd.pipe.sendExit(uint32(1))
		return err
	}
	if err := <-errc; err != nil {
		pCmd.pipe.sendExit(uint32(1))
		return err
	}
	status, err := pCmd.exitStatus(pCmd.cmd.Wait())
	if err != nil {
		pCmd.pipe.sendExit(uint32(1))
		return err
	}
	pCmd.pipe.sendExit(status)
	close(pCmd.pipe.send)
	return nil
}

func (pCmd *pipedCmd) exitStatus(err error) (uint32, error) {
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

func (pipe *Pipe) copyTo(errc chan error, w io.Writer, kind int) {
	_, err := io.Copy(w, pipeIO{pipe: pipe, kind: kind})
	if errc != nil {
		errc <- err
	}
}

func (pipe *Pipe) copyFrom(errc chan error, r io.Reader, kind int) {
	_, err := io.Copy(pipeIO{pipe: pipe, kind: kind}, r)
	if errc != nil {
		errc <- err
	}
}

func (pipe *Pipe) Interact() (int, error) {
	go func() {
		_, err := io.Copy(pipeIO{pipe: pipe, kind: STDIN}, os.Stdin)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}()
	var exitCode int
loop:
	for {
		_, p, err := pipe.conn.ReadMessage()
		if err != nil {
			return 1, fmt.Errorf("reading message: %s", err)
		}
		m, err := DecodeMessage(p)
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

func (pipe *Pipe) Close(msg string) {
	pipe.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
	)
	pipe.conn.Close()
}
