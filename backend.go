package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// hop by hop
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

type Server struct {
	URL     string
	client  *http.Client
	healthy *atomic.Bool
}

func (bs *Server) CheckHealth() bool {
	resp, err := bs.client.Get(bs.URL + "/ping")
	if err != nil {
		return false
	}

	if resp.StatusCode != 200 {
		return false
	}

	return true

}

func HealthCheckLoop(ctx context.Context, bs *Server) {
	for {
		healthy := bs.CheckHealth()
		bs.healthy.Store(healthy)

		select {
			case <- time.After(5 * time.Second):
	

			case <-ctx.Done():
				return
		}


	}
}

func (bs Server) ForwardRequest(r *http.Request) (*http.Response, error) {

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
	servers []*Server
	current atomic.Int32
	ctx    context.Context
}

func NewBackendManager(ctx context.Context) *BackendManager { // constructor
	bm := BackendManager{
		ctx: ctx,
	}
	return &bm
}

func (bm *BackendManager) AddBackend(url string) {
	new_server := &Server{
		URL:    url,
		client: &http.Client{},
		healthy: &atomic.Bool{},
	}
	
	go HealthCheckLoop(bm.ctx, new_server)
	
	bm.servers = append(bm.servers, new_server)
}

func (bm *BackendManager) getBackend(r *http.Request) (*Server, error) {
	// idx := bm.current.Add(1) % int32(len(bm.servers))

	for idx := 0; idx < len(bm.servers); idx++ {
		server := bm.servers[bm.current.Add(1) % int32(len(bm.servers))]

		if server.healthy.Load() {
			return server, nil
		}
	}

	return nil, fmt.Errorf("no healthy backend available")
}