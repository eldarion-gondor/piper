package piper

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/eldarion-gondor/piper/piperc"

	"github.com/gorilla/websocket"
)

type pipeManager struct {
	pipes map[string]*Pipe
	mutex sync.RWMutex
}

var pm *pipeManager

type connector struct {
	label string
	conn  *websocket.Conn
	chIn  chan []byte
	chOut chan []byte
}

type Pipe struct {
	a       *connector
	b       *connector
	done    chan error
	key     string
	running bool
	ready   bool
}

func init() {
	pm = &pipeManager{
		pipes: make(map[string]*Pipe),
	}
}

func NewPipe(key string) *Pipe {
	var pipe *Pipe
	if _, ok := pm.pipes[key]; !ok {
		pm.mutex.RLock()
		pipe = &Pipe{
			done: make(chan error, 2),
			key:  key,
		}
		pm.pipes[key] = pipe
		pm.mutex.RUnlock()
	} else {
		pipe = pm.pipes[key]
	}
	go pipe.Run()
	return pipe
}

func (pipe *Pipe) Run() {
	if pipe.running {
		return
	}
	pipe.running = true
	log.Printf("[%s] start", pipe.key)
	defer func() {
		log.Printf("[%s] stop", pipe.key)
		delete(pm.pipes, pipe.key)
	}()
	var stopped int
	for {
		if pipe.a != nil && pipe.b != nil {
			pipe.signalReady()
			select {
			case m := <-pipe.a.chIn:
				pipe.b.chOut <- m
			case m := <-pipe.b.chIn:
				pipe.a.chOut <- m
			case <-pipe.done:
				if stopped++; stopped == 2 {
					return
				}
			}
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func (pipe *Pipe) signalReady() error {
	if pipe.ready {
		return nil
	}
	pipe.ready = true
	ready := &piperc.Message{Kind: piperc.READY}
	m, err := ready.Prepare()
	if err != nil {
		return err
	}
	pipe.a.conn.WriteMessage(websocket.BinaryMessage, m)
	pipe.b.conn.WriteMessage(websocket.BinaryMessage, m)
	return nil
}

func (pipe *Pipe) copy(c *connector) {
	defer func() {
		close(c.chOut)
		c.conn.Close()
		pipe.done <- nil
	}()
	writer := func() {
		ticker := time.NewTicker((60 * time.Second * 9) / 10)
		defer ticker.Stop()
		write := func(kind int, payload []byte) error {
			c.conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
			return c.conn.WriteMessage(kind, payload)
		}
		for {
			select {
			case m, ok := <-c.chOut:
				if !ok {
					write(websocket.CloseMessage, []byte{})
					return
				}
				if err := write(websocket.BinaryMessage, m); err != nil {
					return
				}
			case <-ticker.C:
				if err := write(websocket.PingMessage, []byte{}); err != nil {
					return
				}
			}
		}
	}
	reader := func() {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		c.conn.SetPongHandler(func(string) error {
			log.Printf("[%s; %s] pong", pipe.key, c.label)
			c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			return nil
		})
		for {
			_, m, err := c.conn.ReadMessage()
			if err != nil {
				if err != io.EOF {
					log.Printf("[%s; %s] reader error: %s\n", pipe.key, c.label, err)
				}
				break
			}
			c.chIn <- m
		}
	}
	go writer()
	reader()
}

func (pipe *Pipe) Copy(conn *websocket.Conn) error {
	var c *connector
	if pipe.a == nil {
		log.Printf("[%s] setup connector A", pipe.key)
		c = makeConnector(conn, "A")
		pipe.a = c
	} else {
		if pipe.b == nil {
			log.Printf("[%s] setup connector B", pipe.key)
			c = makeConnector(conn, "B")
			pipe.b = c
		}
	}
	if c == nil {
		return fmt.Errorf("pipe %q is full", pipe.key)
	}
	pipe.copy(c)
	return nil
}

func makeConnector(conn *websocket.Conn, label string) *connector {
	return &connector{
		label: label,
		conn:  conn,
		chIn:  make(chan []byte),
		chOut: make(chan []byte),
	}
}
