// +build !windows

package piper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/gorilla/websocket"

	"github.com/kr/pty"
)

type pipedCmd struct {
	pipe   *Pipe
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
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
	// read from the pipe and write to stdin of exec'd proccess
	go func() {
		pCmd.pipe.writeTo(pCmd.stdin, STDIN)
		pCmd.pipe.conn.NextReader()
	}()
	// read from stdout/stderr and write to the pipe
	go pCmd.pipe.readFrom(pCmd.stdout, STDOUT)
	if pCmd.stderr != nil {
		go pCmd.pipe.readFrom(pCmd.stderr, STDERR)
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
