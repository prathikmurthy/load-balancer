An HTTP reverse proxy and load balancer written in Go.

## Features

- **Three routing strategies** (compile-time switch in `main.go`):
  - `RoundRobin` — cycles through backends in order
  - `LeastConnections` — routes to the backend with the fewest active connections
  - `ConsistentHash` — routes a given client IP to the same backend via a virtual-node hash ring
- **Active health checks** — each backend is polled every 5 seconds; unhealthy backends are skipped automatically
- **Graceful shutdown** — on `SIGTERM`/`SIGINT`, drains in-flight requests before exiting
- **Hop-by-hop header stripping** and `X-Forwarded-For` forwarding

## Running locally

```bash
go run .
# defaults to three backends on localhost:9001-9003
```

Pass backend URLs as positional arguments to override:

```bash
go run . http://host1:9001 http://host2:9002
```

## Running with Docker

```bash
docker compose up --build
```

Starts the load balancer on `:8080` and three backend instances on ports 9001–9003.