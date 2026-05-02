package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the report service
type Config struct {
	GRPCPort       int    `envconfig:"GRPC_PORT" default:"50055"`
	MetricsPort    int    `envconfig:"METRICS_PORT" default:"8085"`
	DatabaseURL    string `envconfig:"DATABASE_URL" default:"postgres://soundmap:soundmap_secret@localhost:5432/soundmap?sslmode=disable"`
	RedisAddr      string `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	RedisPassword  string `envconfig:"REDIS_PASSWORD" default:"redis_secret"`
	NATSUrl        string `envconfig:"NATS_URL" default:"nats://localhost:4222"`
	JWTSecretKey   string `envconfig:"JWT_SECRET_KEY" default:"soundmap-dev-secret-key-change-in-prod"`
	OTLPEndpoint   string `envconfig:"OTLP_ENDPOINT" default:"localhost:4317"`
	ServiceName    string `envconfig:"SERVICE_NAME" default:"report"`
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
