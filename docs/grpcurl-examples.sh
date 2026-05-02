#!/bin/bash
# SoundMap gRPC Examples using grpcurl
# This script demonstrates how to interact with all SoundMap services

# Prerequisites: install grpcurl
#   go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
#   OR
#   brew install grpcurl

# Generate a test JWT token (requires the auth package)
# For manual testing, use this pre-generated token (expires in 1 hour from generation):
# eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0LXVzZXIiLCJyb2xlIjoiYWRtaW4iLCJleHAiOjE3MTQ2NDY0MDAsImlhdCI6MTcxNDY0MjgwMH0.test-signature

JWT_TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ0ZXN0LXVzZXIiLCJyb2xlIjoiYWRtaW4iLCJleHAiOjE4MjUwMDAwMDB9.test"
INGESTOR_ADDR="localhost:50051"
ANALYZER_ADDR="localhost:50052"
ALERT_ADDR="localhost:50053"
GEO_ADDR="localhost:50054"
REPORT_ADDR="localhost:50055"

echo "======================================"
echo "SoundMap gRPC API Examples"
echo "======================================"
echo ""

# Helper function for grpcurl calls
grpc_call() {
    local addr=$1
    local service=$2
    local method=$3
    local data=$4
    
    grpcurl -plaintext \
        -H "Authorization: Bearer ${JWT_TOKEN}" \
        -d "${data}" \
        "${addr}" \
        "${service}/${method}"
}

echo "1. INGESTOR - Ingest Single Reading (Unary)"
echo "--------------------------------------"
grpc_call "${INGESTOR_ADDR}" "ingestor.v1.IngestorService" "IngestSingleReading" '{
    "reading": {
        "sensor_id": "sensor-nyc-001",
        "latitude": 40.7128,
        "longitude": -74.0060,
        "decibel_level": 75.5,
        "frequency_hz": 440.0,
        "sensor_type": "SENSOR_TYPE_FIXED",
        "raw_samples": [0.1, 0.2, 0.3, 0.4, 0.5]
    }
}'
echo ""

echo "2. INGESTOR - Get Sensor Status (Unary)"
echo "--------------------------------------"
grpc_call "${INGESTOR_ADDR}" "ingestor.v1.IngestorService" "GetSensorStatus" '{
    "sensor_id": "sensor-nyc-001"
}'
echo ""

echo "3. ANALYZER - Analyze Batch (Unary)"
echo "--------------------------------------"
grpc_call "${ANALYZER_ADDR}" "analyzer.v1.AnalyzerService" "AnalyzeBatch" '{
    "zone_id": "zone-manhattan",
    "readings": [
        {
            "reading_id": "read-001",
            "sensor_reading": {
                "sensor_id": "sensor-nyc-001",
                "latitude": 40.7128,
                "longitude": -74.0060,
                "decibel_level": 85.0,
                "frequency_hz": 440.0
            },
            "raw_samples": [0.5, 0.6, 0.7, 0.8, 0.9, 1.0, 0.9, 0.8],
            "sample_rate_hz": 44100
        },
        {
            "reading_id": "read-002",
            "sensor_reading": {
                "sensor_id": "sensor-nyc-002",
                "latitude": 40.7580,
                "longitude": -73.9855,
                "decibel_level": 65.0,
                "frequency_hz": 220.0
            },
            "raw_samples": [0.1, 0.2, 0.1, 0.2, 0.1, 0.2, 0.1, 0.2],
            "sample_rate_hz": 44100
        }
    ]
}'
echo ""

echo "4. ALERT - Create Threshold (Unary)"
echo "--------------------------------------"
grpc_call "${ALERT_ADDR}" "alert.v1.AlertEngineService" "CreateThreshold" '{
    "name": "NYC Warning Threshold",
    "h3_index": "8928308280fffff",
    "max_decibel": 75.0,
    "severity": "ALERT_SEVERITY_WARNING"
}'
echo ""

echo "5. ALERT - List Thresholds (Unary)"
echo "--------------------------------------"
grpc_call "${ALERT_ADDR}" "alert.v1.AlertEngineService" "ListThresholds" '{
    "page": {
        "page": 1,
        "page_size": 10
    }
}'
echo ""

echo "6. GEO - Query Hotspots (Unary)"
echo "--------------------------------------"
grpc_call "${GEO_ADDR}" "geo.v1.GeoIndexService" "QueryHotspots" '{
    "center": {
        "latitude": 40.7128,
        "longitude": -74.0060
    },
    "radius_km": 5.0,
    "resolution": 8,
    "limit": 10,
    "min_decibel": 70.0
}'
echo ""

echo "7. GEO - Get Hex Cell Stats (Unary)"
echo "--------------------------------------"
grpc_call "${GEO_ADDR}" "geo.v1.GeoIndexService" "GetHexCellStats" '{
    "h3_index": "8928308280fffff",
    "time_range_hours": 24
}'
echo ""

echo "8. REPORT - Generate Zone Report (Unary)"
echo "--------------------------------------"
grpc_call "${REPORT_ADDR}" "report.v1.ReportService" "GenerateZoneReport" '{
    "h3_index": "8928308280fffff",
    "start_time": "2024-01-01T00:00:00Z",
    "end_time": "2024-01-02T00:00:00Z"
}'
echo ""

echo "9. INGESTOR - Ingest Readings (Client Streaming)"
echo "--------------------------------------"
echo "This streams multiple readings in a single connection."
echo "Command:"
echo "grpcurl -plaintext -H 'Authorization: Bearer ${JWT_TOKEN}' \\"
echo "  -d '{"reading": {"sensor_id": "sensor-001", ...}}' \\"
echo "  ${INGESTOR_ADDR} ingestor.v1.IngestorService/IngestReadings"
echo ""
echo "(Use the -d flag multiple times for each message in the stream)"
echo ""

echo "10. ANALYZER - Analyze Stream (Bidirectional Streaming)"
echo "--------------------------------------"
echo "This requires sending multiple requests and receiving multiple responses."
echo "Command:"
echo "grpcurl -plaintext -H 'Authorization: Bearer ${JWT_TOKEN}' \\"
echo "  -d '{"reading_id": "1", "sensor_reading": {...}, "raw_samples": [...]}' \\"
echo "  ${ANALYZER_ADDR} analyzer.v1.AnalyzerService/AnalyzeStream"
echo ""

echo "11. ALERT - Subscribe to Alerts (Server Streaming)"
echo "--------------------------------------"
echo "This maintains a persistent connection receiving alerts."
echo "Command:"
echo "grpcurl -plaintext -H 'Authorization: Bearer ${JWT_TOKEN}' \\"
echo "  -d '{"subscriber_id": "dashboard-1", "h3_cells": ["8928308280fffff"]}' \\"
echo "  ${ALERT_ADDR} alert.v1.AlertEngineService/SubscribeAlerts"
echo ""

echo "======================================"
echo "Health Check Endpoints"
echo "======================================"
echo ""

echo "gRPC Health Check (all services):"
echo "grpcurl -plaintext ${INGESTOR_ADDR} grpc.health.v1.Health/Check"
echo ""

echo "HTTP Health Check (if gateway enabled):"
echo "curl http://localhost:8081/health"
echo ""

echo "======================================"
echo "How to Generate a JWT Token for Testing"
echo "======================================"
echo ""
echo "Option 1: Using the agent binary (requires running services):"
echo "  ./soundmap-agent -jwt-secret='soundmap-dev-secret-key-change-in-prod' -generate-token"
echo ""
echo "Option 2: Using Go code:"
echo "  import 'github.com/soundmap/soundmap/internal/auth'"
echo "  token, err := auth.GenerateToken('user-123', 'admin', secret, time.Hour)"
echo ""
echo "Option 3: Using jwt.io with header:"
echo "  {\"alg\":\"HS256\",\"typ\":\"JWT\"}"
echo "  and payload:"
echo "  {\"sub\":\"test-user\",\"role\":\"admin\",\"exp\":<future-timestamp>}"
echo "  signed with: soundmap-dev-secret-key-change-in-prod"
echo ""
