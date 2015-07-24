# piper

piper is a small library to aide in running a process locally, but actually run it remotely over a WebSocket connection.

Here is an example of its usage in a tool used on Gondor:

	package main

	import (
		"flag"
		"fmt"
		"log"
		"net/http"
		"os"
		"os/exec"
		"strings"

		"github.com/eldarion-gondor/piper"
		"github.com/gorilla/websocket"
	)

	var secret = flag.String("secret", "", "")
	var buildSHA string

	var upgrader = websocket.Upgrader{
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
	}

	func main() {
		flag.Parse()
		if len(flag.Args()) == 0 {
			fmt.Println("no program to exec")
			os.Exit(1)
		}
		done := make(chan bool)
		http.HandleFunc("/ok", func(rw http.ResponseWriter, req *http.Request) {
			rw.WriteHeader(http.StatusOK)
		})
		http.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
			defer func() { done <- true }()
			log.Println("connection recieved")
			ws, err := upgrader.Upgrade(rw, req, nil)
			if err != nil {
				log.Printf("upgrade error: %s", err)
				return
			}
			log.Println("upgraded to websockets; ready to pipe")
			pipe, err := piper.NewServerPipe(req, ws)
			if err != nil {
				log.Printf("pipe error: %s", err)
				ws.Close()
				return
			}
			log.Printf("exec: %s", strings.Join(flag.Args(), " "))
			cmd := exec.Command(flag.Arg(0), flag.Args()[1:]...)
			if err := pipe.RunCmd(cmd); err != nil {
				log.Printf("exec error: %s", err)
				pipe.Close("error")
				return
			}
		})
		go func() {
			if err := http.ListenAndServe(":8000", nil); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}()
		<-done
	}

This program will start an HTTP server and expose a process via a WebSocket connection.
