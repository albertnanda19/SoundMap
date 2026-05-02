# Multi-stage build for Alert Engine Service
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY services/alert-engine/go.mod services/alert-engine/go.sum ./services/alert-engine/

RUN go mod download

COPY services/alert-engine/ ./services/alert-engine/
COPY migrations/ ./migrations/
COPY gen/ ./gen/

WORKDIR /app/services/alert-engine
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /service ./cmd/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /service /service
COPY --from=builder /app/migrations /migrations

EXPOSE 50053 8083

USER nonroot

ENTRYPOINT ["/service"]
