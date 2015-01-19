package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/eldarion-gondor/piper"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", ":5661", "http service address")

func main() {
	flag.Parse()
	router := mux.NewRouter()
	router.HandleFunc("/{key}", servePipe)
	http.Handle("/", router)
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
}

func servePipe(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	pipe := piper.NewPipe(vars["key"])
	pipe.Copy(ws)
}
