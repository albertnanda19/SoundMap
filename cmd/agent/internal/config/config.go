package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

// CityCenter holds the geographic center coordinates
type CityCenter struct {
	Lat float64 `envconfig:"LAT" default:"40.7128"`
	Lng float64 `envconfig:"LNG" default:"-74.0060"`
}

// Config holds all configuration for the sensor agent
type Config struct {
	IngestorAddr    string     `envconfig:"INGESTOR_ADDR" default:"localhost:50051"`
	SensorCount     int        `envconfig:"SENSOR_COUNT" default:"50"`
	SendIntervalMs  int        `envconfig:"SEND_INTERVAL_MS" default:"500"`
	DurationSeconds int        `envconfig:"DURATION_SECONDS" default:"0"`
	JWTToken        string     `envconfig:"JWT_TOKEN"`
	JWTSecretKey    string     `envconfig:"JWT_SECRET_KEY" default:"soundmap-dev-secret-key-change-in-prod"`
	TLSEnabled      bool       `envconfig:"TLS_ENABLED" default:"false"`
	TLSCertFile     string     `envconfig:"TLS_CERT_FILE"`
	TLSKeyFile      string     `envconfig:"TLS_KEY_FILE"`
	TLSCAFile       string     `envconfig:"TLS_CA_FILE"`
	ScenarioName    string     `envconfig:"SCENARIO_NAME" default:"mixed"`
	CityCenter      CityCenter `envconfig:"CITY_CENTER"`
	RadiusKm        float64    `envconfig:"RADIUS_KM" default:"5.0"`
	LogLevel        string     `envconfig:"LOG_LEVEL" default:"info"`
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("config.Load: %w", err)
	}
	return &cfg, nil
}
