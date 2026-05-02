package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the ingestor service
type Config struct {
	GRPCPort       int    `envconfig:"GRPC_PORT" default:"50051"`
	MetricsPort    int    `envconfig:"METRICS_PORT" default:"8081"`
	DatabaseURL    string `envconfig:"DATABASE_URL" default:"postgres://soundmap:soundmap_secret@timescaledb:5433/soundmap?sslmode=disable"`
	RedisAddr      string `envconfig:"REDIS_ADDR" default:"redis:6379"`
	RedisPassword  string `envconfig:"REDIS_PASSWORD" default:"redis_secret"`
	NATSUrl        string `envconfig:"NATS_URL" default:"nats://nats:4222"`
	JWTSecretKey   string `envconfig:"JWT_SECRET_KEY" default:"soundmap-dev-secret-key-change-in-prod"`
	OTLPEndpoint   string `envconfig:"OTLP_ENDPOINT" default:"otel-collector:4317"`
	ServiceName    string `envconfig:"SERVICE_NAME" default:"ingestor"`
	ServiceVersion string `envconfig:"SERVICE_VERSION" default:"0.1.0"`
	LogLevel       string `envconfig:"LOG_LEVEL" default:"info"`
	TLSEnabled     bool   `envconfig:"TLS_ENABLED" default:"false"`
	TLSCertFile    string `envconfig:"TLS_CERT_FILE"`
	TLSKeyFile     string `envconfig:"TLS_KEY_FILE"`
	TLSCAFile      string `envconfig:"TLS_CA_FILE"`
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}
	return &cfg, nil
}
