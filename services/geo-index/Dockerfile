# Multi-stage build for Geo Index Service
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY services/geo-index/go.mod services/geo-index/go.sum ./services/geo-index/

RUN go mod download

COPY services/geo-index/ ./services/geo-index/
COPY migrations/ ./migrations/
COPY gen/ ./gen/

WORKDIR /app/services/geo-index
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /service ./cmd/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /service /service
COPY --from=builder /app/migrations /migrations

EXPOSE 50054 8084

USER nonroot

ENTRYPOINT ["/service"]
