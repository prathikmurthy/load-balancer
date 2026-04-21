package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
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
		backendManager.instrumentation.totalRequests.Add(r.Context(), 1)

		server, err := backendManager.getBackend(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer server.connections.Add(-1)
		defer server.drain.Done()

		resp, err := server.ForwardRequest(r)
		if err != nil {
			backendManager.instrumentation.totalFailedRequests.Add(r.Context(), 1)
			http.Error(w, "Error forwarding request to backend", http.StatusBadGateway)
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
			backendManager.instrumentation.totalFailedRequests.Add(r.Context(), 1)
			return
		}

	}
}

func main() {

	http_server := &http.Server{
		Addr: ":8080",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	otelShutdown, err := setupOTelSDK(context.Background())
	if err != nil {
		log.Fatalf("Failed to set up OpenTelemetry SDK: %v", err)
	}

	defer func() {
		otelCtx, otelCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer otelCancel()
		otelShutdownErr := otelShutdown(otelCtx)
		if otelShutdownErr != nil {
			log.Printf("Error during OpenTelemetry SDK shutdown: %v", otelShutdownErr)
		}
	}()

	backendManager := NewBackendManager(ctx, LeastConnections)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-sigs
		log.Println("Shutting down gracefully...")
		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer timeoutCancel()

		http_server.Shutdown(timeoutCtx)
		backendManager.Shutdown()
		os.Exit(0)
	}()

	flag.Parse()
	backends := flag.Args()

	if len(backends) == 0 {
		backends = []string{"http://localhost:9001", "http://localhost:9002", "http://localhost:9003"}
	}
	for _, b := range backends {
		backendManager.AddBackend(b)
	}

	http.HandleFunc("/ping", ping)
	http.HandleFunc("/", server(backendManager))
	http.Handle("/metrics", promhttp.Handler())

	err = http_server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
