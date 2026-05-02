# Multi-stage build for CLI Sensor Agent
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY cmd/agent/go.mod cmd/agent/go.sum ./cmd/agent/

RUN go mod download

COPY cmd/agent/ ./cmd/agent/
COPY gen/ ./gen/

WORKDIR /app/cmd/agent
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /agent ./main.go

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /agent /agent

USER nonroot

ENTRYPOINT ["/agent"]
