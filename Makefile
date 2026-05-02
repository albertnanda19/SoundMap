.PHONY: proto proto-lint proto-breaking infra-up infra-down infra-logs certs build test test-coverage lint run-ingestor run-analyzer run-alert run-geo run-report run-agent run-gateway health smoke-test integration-test migrate clean seed-data

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

# Data seeding
seed-data:
	@echo "Seeding realistic NYC sensor data..."
	@go run scripts/seed-realistic-data/main.go

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

# Health check
health:
	@echo "Checking service health..."
	@echo "========================================"
	@echo "Ingestor:    $$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/health 2>/dev/null || echo "DOWN")"
	@echo "Analyzer:    $$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8082/health 2>/dev/null || echo "DOWN")"
	@echo "Alert Engine:$$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8083/health 2>/dev/null || echo "DOWN")"
	@echo "Geo Index:   $$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8084/health 2>/dev/null || echo "DOWN")"
	@echo "Report:      $$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8085/health 2>/dev/null || echo "DOWN")"
	@echo "Gateway:     $$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/health 2>/dev/null || echo "DOWN")"
	@echo "========================================"

# Smoke test - quick end-to-end test
smoke-test:
	@echo "Running smoke test..."
	@echo "Starting agent for 5 seconds..."
	@cd cmd/agent && timeout 5 go run main.go || true
	@echo "Smoke test complete"

# Integration tests
integration-test:
	@echo "Running integration tests..."
	cd tests/integration && INTEGRATION_TEST=true go test -v ./...

# Database migrations
migrate:
	@echo "Running database migrations..."
	@echo "Migrations are run automatically at service startup"

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
