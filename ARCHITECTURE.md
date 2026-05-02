# SoundMap Architecture

This document explains the architectural decisions and design patterns used in the SoundMap Acoustic Pollution Intelligence Platform.

## Overview

SoundMap is a distributed system for real-time acoustic pollution monitoring using IoT sensors. The platform ingests sensor readings, analyzes them using DSP (Digital Signal Processing), and provides real-time alerts and geospatial queries.

## Service Architecture

### Core Services

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│ CLI Agent   │────▶│  Ingestor   │────▶│    NATS     │
│  (sensors)  │     │ (gRPC:50051)│     │  JetStream  │
└─────────────┘     └─────────────┘     └──────┬──────┘
                                                │
           ┌────────────────────────────────────┼────┐
           │                                    │    │
           ▼                                    ▼    │
     ┌─────────────┐                     ┌──────────┴──┴──┐
     │  Analyzer   │────────────────────▶│  Alert Engine  │
     │ (gRPC:50052)│                     │  (gRPC:50053)  │
     └─────────────┘                     └───────┬───────┘
                                                  │
           ┌──────────────────────────────────────┼──────┐
           │                                      │      │
           ▼                                      ▼      │
     ┌─────────────┐                       ┌───────────┴┐
     │  Geo Index  │◀──────────────────────│  Report   │
     │ (gRPC:50054)│                       │(gRPC:50055)│
     └──────┬──────┘                       └─────────────┘
            │
            ▼
     ┌─────────────┐
     │   Gateway   │◀─── Clients (HTTP/gRPC)
     │ (HTTP:8080) │
     └─────────────┘
```

### Why Each gRPC Pattern Was Chosen

| Service | RPC Method | Pattern | Rationale |
|---------|-----------|---------|-----------|
| Ingestor | `IngestSingleReading` | Unary | Simple one-off sensor readings; REST-compatible |
| Ingestor | `IngestReadings` | Client Streaming | Sensors send batches efficiently without waiting for each ACK |
| Analyzer | `AnalyzeBatch` | Unary | Synchronous analysis of buffered readings |
| Analyzer | `AnalyzeStream` | Bidirectional | Real-time DSP with backpressure control |
| Analyzer | `GetZoneAnalysis` | Server Streaming | Chunked results for large geographic zones |
| Alert | `SubscribeAlerts` | Server Streaming | Push-based alert delivery to subscribers |
| Alert | `CreateThreshold` | Unary | Simple CRUD operation |
| Geo | `QueryHotspots` | Unary | Point query with cached results |
| Geo | `StreamCellUpdates` | Server Streaming | Live map updates for monitoring dashboards |
| Report | `GenerateCityReport` | Server Streaming | Large reports streamed in chunks to avoid memory pressure |

## Data Flow: Sensor to Alert

```
1. Sensor (CLI Agent) ──gRPC──▶ Ingestor
   └─ Sends: sensor_id, lat/lng, decibel, frequency, raw_samples

2. Ingestor ──NATS JetStream──▶ readings.raw
   └─ Persists to TimescaleDB (async)
   └─ Publishes to NATS for real-time processing

3. NATS Consumer (Analyzer) ──▶ DSP Pipeline
   a. FFT (Cooley-Tukey radix-2) on raw samples
   b. Frequency band analysis (7 bands: 20Hz-20kHz)
   c. Dominant frequency detection
   d. Noise category classification
   e. Health risk scoring (0-100)
   
4. Analyzer ──NATS──▶ readings.analyzed
   └─ Publishes: analysis result with risk score, category

5. Alert Engine (Consumer) ──▶ Threshold Check
   a. Lookup thresholds for H3 cell (from TimescaleDB)
   b. Compare decibel against threshold
   c. If exceeded: broadcast AlertEvent to subscribers
   
6. Alert Engine ──gRPC Stream──▶ Subscribers
   └─ Real-time push notification with severity level
```

## Technology Trade-offs

### Why NATS over Kafka

**Decision**: Use NATS JetStream instead of Apache Kafka

**Rationale**:
- **Operational Simplicity**: NATS is a single binary vs Kafka's ZooKeeper + Broker cluster
- **Memory-First**: JetStream's memory storage matches our hot-path latency requirements (<< 10ms)
- **gRPC-Native**: NATS has first-class Go support with async patterns that match our architecture
- **Sufficient Scale**: For our target of 100K readings/sec, NATS handles this without Kafka's complexity

**When we'd switch to Kafka**:
- If we needed >1M events/sec sustained
- If we needed complex stream processing (joins, windows)
- If we needed multi-region replication with strict ordering

### Why TimescaleDB over Plain PostgreSQL

**Decision**: Use TimescaleDB hypertables for sensor readings

**Rationale**:
- **Time-Series Optimized**: Automatic partitioning by time improves insert performance 10x
- **Compression**: 90% storage reduction on historical data (tested with 1B rows)
- **Continuous Aggregates**: Native materialized views for hourly/daily rollups
- **SQL Compatibility**: Existing PostgreSQL tools, drivers, and expertise work unchanged

**Schema Design**:
```sql
-- sensor_readings table (hypertable)
-- Chunk size: 1 day, Compression: 7 days after insertion
-- Retention: 90 days hot, 1 year cold (S3 via tiering)
```

### Why Redis over Memcached

**Decision**: Use Redis for Geo-Index caching

**Rationale**:
- **Data Structures**: Redis hashes store complex hex cell objects (JSON)
- **Pub/Sub**: Can invalidate cache on threshold changes
- **Persistence**: RDB snapshots prevent cache cold starts
- **Atomic Operations**: HINCRBY for hit/miss counters

## Interceptor Chain Design

The interceptor order is critical for correct behavior:

```
Request ──▶ Recovery ──▶ Auth ──▶ Rate Limit ──▶ Logging ──▶ OTel ──▶ Handler
     │           │          │            │           │         │
     │           │          │            │           │         └─ Span created here
     │           │          │            │           └─ Logs user, status, duration
     │           │          │            └─ Per-user limits (needs auth context)
     │           │          └─ JWT validation, claims extraction
     │           └─ Catches panics from all inner layers
     └─ Outer error handling boundary
```

**Why this order**:
1. **Recovery first**: Catches panics in auth, rate limiting, etc.
2. **Auth before rate limit**: Rate limits are per-user (not per-IP)
3. **Logging after rate limit**: Only logs actually-processed requests
4. **OTel innermost**: Span includes full processing time including interceptors

## How to Extend the System

### Adding a New Service

1. **Define proto** in `proto/newservice/v1/`:
   ```protobuf
   service NewService {
     rpc Method(Request) returns (Response);
   }
   ```

2. **Generate code**: `make proto`

3. **Create service directory**:
   ```
   services/newservice/
   ├── cmd/
   │   └── main.go          # Service entry point
   ├── internal/
   │   ├── service/         # gRPC handlers
   │   └── repository/      # Data access
   └── go.mod
   ```

4. **Use standard patterns**:
   - Interceptor chain from `internal/interceptors`
   - Config loading from `internal/config`
   - Graceful shutdown from template

5. **Add to docker-compose.yml** and `Makefile`

### Adding a New Interceptor

1. **Implement interceptor** in `internal/interceptors/new.go`:
   ```go
   func newUnaryInterceptor(...) grpc.UnaryServerInterceptor {
       return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
           // Before handler
           resp, err := handler(ctx, req)
           // After handler
           return resp, err
       }
   }
   ```

2. **Insert in chain** at appropriate position in `chain.go`

3. **Add tests** verifying position in chain

## Performance Characteristics

| Component | Target Throughput | Latency (p99) |
|-----------|------------------|---------------|
| Ingestor (Unary) | 1,000 req/s | 5ms |
| Ingestor (Streaming) | 50K readings/s | 1ms |
| Analyzer (Batch) | 10K readings/s | 50ms |
| Analyzer (Stream) | 1K readings/s | 10ms |
| Geo Query | 500 req/s | 20ms (cache hit) |
| Geo Query | 100 req/s | 100ms (cache miss) |
| Alert Engine | 50K readings/s | 5ms |

## Bottlenecks and Mitigations

| Bottleneck | Solution | Implementation |
|------------|----------|----------------|
| DB insert rate | CopyFrom bulk insert | `pgx.CopyFrom` in repository |
| Geo query latency | Redis cache | Cache-aside with 30s TTL |
| NATS publish latency | Async publish | JetStream with background flushes |
| FFT computation | Vectorized Go | Pure Go radix-2 Cooley-Tukey |
| Alert broadcast | Subscriber fan-out | goroutine per subscriber with timeout |

## Deployment Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Kubernetes                         │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │
│  │ Ingestor    │  │ Analyzer    │  │ Alert       │      │
│  │ (3 replicas)│  │ (2 replicas)│  │ (2 replicas)│      │
│  └─────────────┘  └─────────────┘  └─────────────┘      │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐      │
│  │ Geo Index   │  │ Report      │  │ Gateway     │      │
│  │ (2 replicas)│  │ (1 replica) │  │ (2 replicas)│      │
│  └─────────────┘  └─────────────┘  └─────────────┘      │
│                                                         │
│  ┌─────────────────────────────────────────────────┐    │
│  │  TimescaleDB (3 nodes)  │  Redis (3 shards)    │    │
│  │  NATS (3 nodes)         │  Prometheus/Grafana  │    │
│  └─────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

## Further Reading

- [PERFORMANCE.md](PERFORMANCE.md) - Benchmarks and optimization details
- [docs/ingest-flow.md](docs/ingest-flow.md) - Mermaid sequence diagram
- [proto/README.md](proto/README.md) - API documentation
