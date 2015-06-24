package piper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/gorilla/websocket"
	"github.com/kr/pty"
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

type pipedCmd struct {
	pipe   *Pipe
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

type Winsize struct {
	Height uint16
	Width  uint16
	x      uint16
	y      uint16
}

func NewClientPipe(host string, opts Opts) (*Pipe, error) {
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
	pipe := &Pipe{
		conn: conn,
		send: make(chan []byte),
		opts: opts,
	}
	go pipe.writer()
	return pipe, nil
}

func NewServerPipe(req *http.Request, conn *websocket.Conn) (*Pipe, error) {
	xPipeOpts := req.Header.Get("X-Pipe-Opts")
	if xPipeOpts == "" {
		return nil, fmt.Errorf("missing X-Pipe-Opts")
	}
	var opts Opts
	if err := json.Unmarshal([]byte(xPipeOpts), &opts); err != nil {
		return nil, err
	}
	pipe := &Pipe{
		conn: conn,
		send: make(chan []byte),
		opts: opts,
	}
	go pipe.writer()
	return pipe, nil
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

func (pipe *Pipe) sendEOF() {
	msg := Message{Kind: EOF}
	payload, _ := msg.Prepare()
	pipe.send <- payload
}

func (pipe *Pipe) RunCmd(cmd *exec.Cmd) error {
	if pipe.opts.Tty {
		return pipe.runPtyCmd(cmd)
	}
	return pipe.runStdCmd(cmd)
}

func (pipe *Pipe) runStdCmd(cmd *exec.Cmd) error {
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

func (pipe *Pipe) runPtyCmd(cmd *exec.Cmd) error {
	py, tty, err := pty.Open()
	if err != nil {
		return err
	}
	defer tty.Close()
	env := os.Environ()
	env = append(env, "TERM=xterm")
	cmd.Env = env
	cmd.Stdout = tty
	cmd.Stdin = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Setsid: true}
	ws := &Winsize{
		Width:  uint16(pipe.opts.Width),
		Height: uint16(pipe.opts.Height),
	}
	_, _, syserr := syscall.Syscall(syscall.SYS_IOCTL, py.Fd(), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(ws)))
	if syserr != 0 {
		return syserr
	}
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
	defer close(pCmd.pipe.send)
	go pCmd.pipe.copyFrom(pCmd.stdin, STDIN)
	go pCmd.pipe.copyTo(pCmd.stdout, STDOUT)
	if pCmd.stderr != nil {
		go pCmd.pipe.copyTo(pCmd.stderr, STDERR)
	}
	if err := pCmd.cmd.Start(); err != nil {
		pCmd.pipe.sendExit(uint32(1))
		return err
	}
	waitErrCh := make(chan error)
	go func() {
		waitErrCh <- pCmd.cmd.Wait()
	}()
	select {
	case err := <-waitErrCh:
		status, err := pCmd.exitStatus(err)
		if err != nil {
			pCmd.pipe.sendExit(uint32(1))
			return err
		}
		pCmd.pipe.sendExit(status)
	}
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

func (pipe *Pipe) copyFrom(w io.WriteCloser, kind int) error {
	defer w.Close()
	_, err := io.Copy(w, pipeIO{pipe: pipe, kind: kind})
	if err != nil {
		return err
	}
	return nil
}

func (pipe *Pipe) copyTo(r io.Reader, kind int) error {
	_, err := io.Copy(pipeIO{pipe: pipe, kind: kind}, r)
	if err != nil {
		return err
	}
	return nil
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
loop:
	for {
		_, r, err := pipe.conn.NextReader()
		if err != nil {
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

func (pipe *Pipe) Close(msg string) {
	pipe.conn.WriteMessage(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, msg),
	)
	pipe.conn.Close()
}
