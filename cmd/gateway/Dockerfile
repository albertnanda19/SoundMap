# Multi-stage build for API Gateway
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY cmd/gateway/go.mod cmd/gateway/go.sum ./cmd/gateway/

RUN go mod download

COPY cmd/gateway/ ./cmd/gateway/
COPY gen/ ./gen/
COPY gen/openapi/ ./gen/openapi/

WORKDIR /app/cmd/gateway
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /gateway ./main.go

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /gateway /gateway
COPY --from=builder /app/gen/openapi/ /openapi/

EXPOSE 8080 9090 8086

USER nonroot

ENTRYPOINT ["/gateway"]
