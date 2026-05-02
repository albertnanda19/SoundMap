# Multi-stage build for Ingestor Service
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

# Copy root files first for shared dependencies
COPY go.mod go.sum ./
COPY gen/ ./gen/

# Copy service files
COPY services/ingestor/go.mod services/ingestor/go.sum ./services/ingestor/
COPY services/ingestor/ ./services/ingestor/
COPY migrations/ ./migrations/

# Download dependencies - only the service module
WORKDIR /app/services/ingestor
RUN go mod download

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /service ./cmd/

# Final stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1001 nonroot

COPY --from=builder /service /service
COPY --from=builder /app/migrations /migrations

EXPOSE 50051 8081

USER nonroot

ENTRYPOINT ["/service"]