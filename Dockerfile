FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o lb .
RUN go build -o backend ./cmd/backend

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/lb .
COPY --from=builder /app/backend .
