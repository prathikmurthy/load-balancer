package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	port := flag.Int("port", 9001, "port to listen on")
	flag.Parse()

	addr := fmt.Sprintf(":%d", *port)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"port":   *port,
			"method": r.Method,
			"path":   r.URL.Path,
		})
	})

	http.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received slow request: %s %s", r.Method, r.URL.Path)
		time.Sleep(5 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"port":   *port,
			"method": r.Method,
			"path":   r.URL.Path,
			"slow":   true,
		})
	})

	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Recieved Ping Request: %s %s", r.Method, r.URL.Path)
		json.NewEncoder(w).Encode(map[string]any{
			"ping": "pong",
			"port":   *port,
			"method": r.Method,
			"path":   r.URL.Path,
		})
	})
	

	log.Printf("backend listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
