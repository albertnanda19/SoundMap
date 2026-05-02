#!/bin/bash
# Build script for individual services

set -e

echo "Building soundmap microservices..."

# Create build directory
mkdir -p build

cd services/ingestor
echo "Building ingestor..."
CGO_ENABLED=0 GOOS=linux go build -o ../../build/ingestor ./cmd/
cd ../..

cd services/analyzer
echo "Building analyzer..."
CGO_ENABLED=0 GOOS=linux go build -o ../../build/analyzer ./cmd/
cd ../..

cd services/alert-engine
echo "Building alert-engine..."
CGO_ENABLED=0 GOOS=linux go build -o ../../build/alert-engine ./cmd/
cd ../..

cd services/geo-index
echo "Building geo-index..."
CGO_ENABLED=0 GOOS=linux go build -o ../../build/geo-index ./cmd/
cd ../..

cd services/report
echo "Building report..."
CGO_ENABLED=0 GOOS=linux go build -o ../../build/report ./cmd/
cd ../..

echo "All services built successfully"
ls -la build/