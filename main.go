package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

type Response struct {
	Message string `json:"message"`
}

type BackendServer struct {
	URL    string
	client *http.Client
}

var HBH_HEADERS = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func (bs BackendServer) ForwardRequest(r *http.Request) (*http.Response, error) {
	
	route := r.URL.Path
	params := r.URL.Query().Encode()
	if len(params) > 0 {
		route += "?" + params
	}

	new_url := bs.URL + route
	
	new_req, err := http.NewRequest(r.Method, new_url, r.Body)
	if err != nil {
		return nil, err
	}

	new_req.Header = r.Header.Clone()
	custom_hbh := strings.Split(r.Header.Get("Connection"), ", ")
	for _, h := range custom_hbh {
		new_req.Header.Del(h)
	}

	for _, h := range HBH_HEADERS {
		new_req.Header.Del(h)
	}

	new_req.Header.Add("X-Forwarded-For", r.RemoteAddr)

	return bs.client.Do(new_req)
}

type BackendManager struct {
	servers []BackendServer
}

func (bm *BackendManager) AddBackend(url string) {
	bm.servers = append(bm.servers, BackendServer{
		URL:    url,
		client: &http.Client{},
	})
}

func (bm *BackendManager) getBackend(r *http.Request) BackendServer {
	return bm.servers[0]
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

	backendManager := &BackendManager{}
	backendManager.AddBackend("http://localhost:9002")

	http.HandleFunc("/ping", ping)
	http.HandleFunc("/", server(backendManager))

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		panic(err)
	}
}