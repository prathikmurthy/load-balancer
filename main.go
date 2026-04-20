package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

type Response struct {
	Message string `json:"message"`
}

func ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(200)
	response := Response{Message: "pong"}
	json.NewEncoder(w).Encode(response)
}

func server(backendManager *BackendManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := backendManager.getBackend(r).ForwardRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		w.WriteHeader(resp.StatusCode)

		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			log.Printf("Error copying response body: %v", err)
			return
		}

	}
}


func main() {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backendManager := NewBackendManager(ctx)
	backendManager.AddBackend("http://localhost:9001")
	backendManager.AddBackend("http://localhost:9002")
	backendManager.AddBackend("http://localhost:9003")

	http.HandleFunc("/ping", ping)
	http.HandleFunc("/", server(backendManager))

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}
