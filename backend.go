package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
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

type ConsistentHashRing struct {
	RingPositions    []uint32
	PositionMap      map[uint32]*Server
	VirtualNodeCount uint8
}

type Server struct {
	URL         string
	client      *http.Client
	healthy     *atomic.Bool
	connections atomic.Int32
	drain       sync.WaitGroup
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
		case <-time.After(5 * time.Second):

		case <-ctx.Done():
			return
		}

	}
}

func (bs *Server) ForwardRequest(r *http.Request) (*http.Response, error) {

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

type OTelInstrumentation struct {
	totalRequests       metric.Int64Counter
	totalFailedRequests metric.Int64Counter
	requestDuration     metric.Float64Histogram
}

func NewOTelInstrumentation(servers *[]*Server) *OTelInstrumentation {
	meter := otel.GetMeterProvider().Meter("backend_manager")

	totalRequests, err := meter.Int64Counter("total_requests")
	if err != nil {
		panic(err)
	}

	totalRequests.Add(context.Background(), 1)
	totalRequests.Add(context.Background(), -1)

	totalFailedRequests, err := meter.Int64Counter("total_failed_requests")
	if err != nil {
		panic(err)
	}

	totalFailedRequests.Add(context.Background(), 1)
	totalFailedRequests.Add(context.Background(), -1)
	
	requestDuration, err := meter.Float64Histogram(
		"request_duration_ms",
		metric.WithDescription("Duration of successfully forwarded requests in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		panic(err)
	}

	_, err = meter.Int64ObservableGauge(
		"backend_active_connections",
		metric.WithDescription("Number of active connections per backend"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for _, s := range *servers {
				o.Observe(int64(s.connections.Load()), metric.WithAttributes(attribute.String("backend", s.URL)))
			}
			return nil
		}),
	)
	if err != nil {
		panic(err)
	}

	_, err = meter.Int64ObservableGauge(
		"backend_health",
		metric.WithDescription("Health status per backend (1=healthy, 0=unhealthy)"),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			for _, s := range *servers {
				val := int64(0)
				if s.healthy.Load() {
					val = 1
				}
				o.Observe(val, metric.WithAttributes(attribute.String("backend", s.URL)))
			}
			return nil
		}),
	)
	if err != nil {
		panic(err)
	}

	return &OTelInstrumentation{
		totalRequests:       totalRequests,
		totalFailedRequests: totalFailedRequests,
		requestDuration:     requestDuration,
	}
}

type BackendManager struct {
	servers         []*Server
	current         atomic.Int32 // idx for round robin
	ctx             context.Context
	strategy        BackendManagerStrategy
	hashRing        *ConsistentHashRing
	instrumentation *OTelInstrumentation
}

type BackendManagerStrategy int

const (
	RoundRobin BackendManagerStrategy = iota
	LeastConnections
	ConsistentHash
)

func NewBackendManager(ctx context.Context, strategy BackendManagerStrategy) *BackendManager { // constructor

	bm := BackendManager{
		ctx:      ctx,
		strategy: strategy,
		hashRing: &ConsistentHashRing{
			VirtualNodeCount: 5,
		},
	}
	bm.instrumentation = NewOTelInstrumentation(&bm.servers)

	return &bm
}

func (bm *BackendManager) Shutdown() {
	for _, server := range bm.servers {
		server.healthy.Store(false)
	}

	for _, server := range bm.servers {
		server.drain.Wait()
	}
}

func (bm *BackendManager) AddBackend(url string) {
	new_server := &Server{
		URL:     url,
		client:  &http.Client{},
		healthy: &atomic.Bool{},
	}

	go HealthCheckLoop(bm.ctx, new_server)

	bm.servers = append(bm.servers, new_server)

	if bm.strategy == ConsistentHash {
		// virtual nodes
		for i := 0; i < int(bm.hashRing.VirtualNodeCount); i++ {
			virtual_node_id := fmt.Sprintf("%s#%d", url, i)
			hash := fnv.New32a()
			hash.Write([]byte(virtual_node_id))
			bm.hashRing.RingPositions = append(bm.hashRing.RingPositions, hash.Sum32())
			bm.hashRing.PositionMap[hash.Sum32()] = new_server
		}

		sort.Slice(bm.hashRing.RingPositions, func(i, j int) bool {
			return bm.hashRing.RingPositions[i] < bm.hashRing.RingPositions[j]
		})
	}
}

func (bm *BackendManager) getBackend(r *http.Request) (*Server, error) {
	// idx := bm.current.Add(1) % int32(len(bm.servers))

	switch bm.strategy {
	case RoundRobin:
		for idx := 0; idx < len(bm.servers); idx++ {
			server := bm.servers[bm.current.Add(1)%int32(len(bm.servers))]

			if server.healthy.Load() {
				server.connections.Add(1)
				server.drain.Add(1)
				return server, nil
			}
		}

		return nil, fmt.Errorf("no healthy backend available")

	case LeastConnections:
		var least_conn_server *Server
		for _, server := range bm.servers {
			if server.healthy.Load() {
				if least_conn_server == nil || server.connections.Load() < least_conn_server.connections.Load() {
					least_conn_server = server
				}
			}
		}

		if least_conn_server != nil {
			least_conn_server.connections.Add(1)
			least_conn_server.drain.Add(1)
			return least_conn_server, nil
		}

		return nil, fmt.Errorf("no healthy backend available")

	case ConsistentHash:
		client_ip := strings.Split(r.RemoteAddr, ":")[0]
		hash := fnv.New32a()

		hash.Write([]byte(client_ip))

		server_index := sort.Search(len(bm.hashRing.RingPositions), func(i int) bool {
			return bm.hashRing.RingPositions[uint32(i)] >= uint32(hash.Sum32())
		})

		if server_index == len(bm.hashRing.RingPositions) {
			server_index = 0
		}

		for i := 0; i < len(bm.hashRing.RingPositions); i++ {
			server := bm.hashRing.PositionMap[bm.hashRing.RingPositions[server_index]]
			if server.healthy.Load() {
				server.connections.Add(1)
				server.drain.Add(1)
				return server, nil
			}

			server_index = (server_index + 1) % len(bm.hashRing.RingPositions)
		}

		return nil, fmt.Errorf("no healthy backend available")

	default:
		return nil, fmt.Errorf("invalid load balancing strategy")
	}

}
