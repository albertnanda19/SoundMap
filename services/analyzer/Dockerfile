# Multi-stage build for Analyzer Service
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.work go.work.sum ./
COPY go.mod go.sum ./
COPY services/analyzer/go.mod services/analyzer/go.sum ./services/analyzer/

RUN go mod download

COPY services/analyzer/ ./services/analyzer/
COPY migrations/ ./migrations/
COPY gen/ ./gen/

WORKDIR /app/services/analyzer
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /service ./cmd/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /service /service
COPY --from=builder /app/migrations /migrations

EXPOSE 50052 8082

USER nonroot

ENTRYPOINT ["/service"]
