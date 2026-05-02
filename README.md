# SoundMap - Acoustic Pollution Intelligence Platform

SoundMap is a real-time acoustic pollution monitoring platform built with Go microservices communicating via gRPC. It ingests sound level data from thousands of simulated IoT sensors, analyzes data using DSP techniques, indexes readings geospatially using Uber's H3 hexagonal grid, triggers alerts when noise thresholds are exceeded, and generates aggregated reports.

## Architecture

### Services

| Service | Port | Description |
|---------|------|-------------|
| Ingestor | 8081 | Ingests sensor readings (client streaming & unary) |
| Analyzer | 8082 | Analyzes audio data with DSP (bidirectional streaming) |
| Alert Engine | 8083 | Manages thresholds and fires alerts |
| Geo Index | 8084 | Geospatial queries with H3 hexagonal grid |
| Report | 8085 | Generates statistical reports |
| Gateway | 8080 | REST/gRPC gateway entry point |

### Infrastructure

- **TimescaleDB**: Time-series database for sensor readings
- **Redis**: Caching and session management
- **NATS**: Message bus for async communication
- **Prometheus**: Metrics collection
- **Grafana**: Metrics visualization
- **Jaeger**: Distributed tracing
- **OpenTelemetry Collector**: Telemetry pipeline

## Quick Start

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- buf (protobuf toolchain)
- protoc plugins

### Setup Development Environment

```bash
# Install development dependencies
make dev-setup

# Generate protobuf code
make proto

# Generate TLS certificates
make certs

# Start infrastructure
make infra-up
```

### Running Services

```bash
# Run individual services
make run-ingestor
make run-analyzer
make run-alert
make run-geo
make run-report

# Or run the CLI agent
make run-agent
```

### Testing

```bash
# Run all tests
make test

# Run with coverage
make test-coverage
```

## Project Structure

```
soundmap/
├── proto/              # Protocol buffer definitions
│   ├── common/v1/      # Shared types
│   ├── ingestor/v1/    # Ingestor service API
│   ├── analyzer/v1/    # Analyzer service API
│   ├── alert/v1/       # Alert service API
│   ├── geo/v1/         # Geo-index service API
│   └── report/v1/      # Report service API
├── services/           # Microservice implementations
├── internal/           # Shared internal packages
├── cmd/                # CLI tools and gateway
├── gen/                # Auto-generated proto code
└── deployments/        # Docker Compose configs
```

## Makefile Commands

| Command | Description |
|---------|-------------|
| `make proto` | Generate protobuf code |
| `make proto-lint` | Lint protobuf files |
| `make infra-up` | Start infrastructure |
| `make infra-down` | Stop infrastructure |
| `make infra-logs` | View infrastructure logs |
| `make certs` | Generate TLS certificates |
| `make build` | Build all services |
| `make test` | Run all tests |
| `make lint` | Run golangci-lint |
| `make clean` | Remove build artifacts |

## License

MIT License - See LICENSE file for details
