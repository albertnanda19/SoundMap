package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the API Gateway
type Config struct {
	HTTPPort           int    `envconfig:"HTTP_PORT" default:"8080"`
	GRPCPort           int    `envconfig:"GRPC_PORT" default:"9090"`
	MetricsPort        int    `envconfig:"METRICS_PORT" default:"8086"`
	IngestorAddr       string `envconfig:"INGESTOR_ADDR" default:"localhost:50051"`
	AnalyzerAddr       string `envconfig:"ANALYZER_ADDR" default:"localhost:50052"`
	AlertAddr          string `envconfig:"ALERT_ADDR" default:"localhost:50053"`
	GeoAddr            string `envconfig:"GEO_ADDR" default:"localhost:50054"`
	ReportAddr         string `envconfig:"REPORT_ADDR" default:"localhost:50055"`
	JWTSecretKey       string `envconfig:"JWT_SECRET_KEY" default:"soundmap-dev-secret-key-change-in-prod"`
	TLSEnabled         bool   `envconfig:"TLS_ENABLED" default:"false"`
	TLSCertFile        string `envconfig:"TLS_CERT_FILE"`
	TLSKeyFile         string `envconfig:"TLS_KEY_FILE"`
	TLSCAFile          string `envconfig:"TLS_CA_FILE"`
	CORSAllowedOrigins string `envconfig:"CORS_ALLOWED_ORIGINS" default:"*"`
	OTLPEndpoint       string `envconfig:"OTLP_ENDPOINT" default:"localhost:4317"`
	ServiceName        string `envconfig:"SERVICE_NAME" default:"gateway"`
	LogLevel           string `envconfig:"LOG_LEVEL" default:"info"`
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}
	return &cfg, nil
}
