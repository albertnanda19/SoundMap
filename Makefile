.PHONY: proto proto-lint proto-breaking infra-up infra-down infra-logs certs build test test-coverage lint run-ingestor run-analyzer run-alert run-geo run-report run-agent migrate clean

# Proto generation
proto:
	@echo "Generating protobuf code..."
	cd proto && buf generate

proto-lint:
	@echo "Linting protobuf files..."
	cd proto && buf lint

proto-breaking:
	@echo "Checking for breaking changes..."
	cd proto && buf breaking --against '.git#branch=main'

# Infrastructure
infra-up:
	@echo "Starting infrastructure..."
	cd deployments && docker compose up -d

infra-down:
	@echo "Stopping infrastructure..."
	cd deployments && docker compose down

infra-logs:
	@echo "Following infrastructure logs..."
	cd deployments && docker compose logs -f

# Certificates
certs:
	@echo "Generating TLS certificates..."
	bash scripts/gen-certs.sh

# Build
build:
	@echo "Building all services..."
	go build -o build/ingestor ./services/ingestor/...
	go build -o build/analyzer ./services/analyzer/...
	go build -o build/alert-engine ./services/alert-engine/...
	go build -o build/geo-index ./services/geo-index/...
	go build -o build/report ./services/report/...
	go build -o build/agent ./cmd/agent/...
	go build -o build/gateway ./cmd/gateway/...

# Testing
test:
	@echo "Running tests..."
	go test ./...

test-coverage:
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Linting
lint:
	@echo "Running linter..."
	golangci-lint run ./...

# Run services
run-ingestor:
	go run ./services/ingestor/...

run-analyzer:
	go run ./services/analyzer/...

run-alert:
	go run ./services/alert-engine/...

run-geo:
	go run ./services/geo-index/...

run-report:
	go run ./services/report/...

run-agent:
	go run ./cmd/agent/...

# Database migrations
migrate:
	@echo "Running database migrations..."
	@echo "Migration not yet implemented"

# Cleanup
clean:
	@echo "Cleaning build artifacts..."
	rm -rf build/
	rm -rf gen/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# Development helpers
dev-setup:
	@echo "Installing development dependencies..."
	go install github.com/bufbuild/buf/cmd/buf@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/grpc-ecosystem/grpc-gateway/protoc-gen-grpc-gateway@latest
	go install github.com/grpc-ecosystem/grpc-gateway/protoc-gen-openapiv2@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
