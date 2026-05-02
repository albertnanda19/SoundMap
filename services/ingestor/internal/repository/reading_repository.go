package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
)

// SensorStatus represents the current status of a sensor
type SensorStatus struct {
	SensorID            string
	LastDecibelLevel    float64
	LastSeenAt          time.Time
	TotalReadingsToday  int64
}

// ReadingRepository defines the interface for storing sensor readings
type ReadingRepository interface {
	SaveReading(ctx context.Context, reading *commonv1.SensorReading) (string, error)
	SaveReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) (int64, error)
	GetSensorLastReading(ctx context.Context, sensorID string) (*SensorStatus, error)
	GetReadingCountToday(ctx context.Context, sensorID string) (int64, error)
}

// PostgresReadingRepository implements ReadingRepository using PostgreSQL/TimescaleDB
type PostgresReadingRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresReadingRepository creates a new repository with connection pooling
func NewPostgresReadingRepository(ctx context.Context, databaseURL string) (*PostgresReadingRepository, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}
	
	config.MaxConns = 10
	config.MinConns = 2
	config.MaxConnLifetime = 5 * time.Minute
	config.MaxConnIdleTime = 1 * time.Minute
	
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}
	
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	
	return &PostgresReadingRepository{pool: pool}, nil
}

// SaveReading stores a single sensor reading and returns the generated reading ID
func (r *PostgresReadingRepository) SaveReading(ctx context.Context, reading *commonv1.SensorReading) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	
	readingID := uuid.New().String()
	recordedAt := reading.Timestamp.AsTime()
	
	query := `
		INSERT INTO sensor_readings (id, sensor_id, latitude, longitude, decibel_level, frequency_hz, sensor_type, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	
	_, err := r.pool.Exec(ctx, query,
		readingID,
		reading.SensorId,
		reading.Latitude,
		reading.Longitude,
		reading.DecibelLevel,
		reading.FrequencyHz,
		reading.SensorType.String(),
		recordedAt,
	)
	if err != nil {
		return "", fmt.Errorf("ReadingRepository.SaveReading: %w", err)
	}
	
	return readingID, nil
}

// SaveReadingsBatch performs bulk insert using CopyFrom for efficiency
func (r *PostgresReadingRepository) SaveReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) (int64, error) {
	if len(readings) == 0 {
		return 0, nil
	}
	
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	
	columns := []string{"id", "sensor_id", "latitude", "longitude", "decibel_level", "frequency_hz", "sensor_type", "recorded_at"}
	
	rows := make([][]interface{}, len(readings))
	for i, reading := range readings {
		rows[i] = []interface{}{
			uuid.New().String(),
			reading.SensorId,
			reading.Latitude,
			reading.Longitude,
			reading.DecibelLevel,
			reading.FrequencyHz,
			reading.SensorType.String(),
			reading.Timestamp.AsTime(),
		}
	}
	
	copyCount, err := r.pool.CopyFrom(ctx, pgx.Identifier{"sensor_readings"}, columns, pgx.CopyFromRows(rows))
	if err != nil {
		return 0, fmt.Errorf("ReadingRepository.SaveReadingsBatch: %w", err)
	}
	
	return copyCount, nil
}

// GetSensorLastReading retrieves the last status of a sensor
func (r *PostgresReadingRepository) GetSensorLastReading(ctx context.Context, sensorID string) (*SensorStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	
	query := `
		SELECT sensor_id, decibel_level, recorded_at
		FROM sensor_readings
		WHERE sensor_id = $1
		ORDER BY recorded_at DESC
		LIMIT 1
	`
	
	var status SensorStatus
	err := r.pool.QueryRow(ctx, query, sensorID).Scan(
		&status.SensorID,
		&status.LastDecibelLevel,
		&status.LastSeenAt,
	)
	if err != nil {
		return nil, fmt.Errorf("ReadingRepository.GetSensorLastReading: %w", err)
	}
	
	// Get today's count
	count, err := r.GetReadingCountToday(ctx, sensorID)
	if err != nil {
		return nil, err
	}
	status.TotalReadingsToday = count
	
	return &status, nil
}

// GetReadingCountToday returns the count of readings for a sensor today
func (r *PostgresReadingRepository) GetReadingCountToday(ctx context.Context, sensorID string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	
	query := `
		SELECT COUNT(*)
		FROM sensor_readings
		WHERE sensor_id = $1
			AND recorded_at >= date_trunc('day', NOW())
			AND recorded_at < date_trunc('day', NOW()) + INTERVAL '1 day'
	`
	
	var count int64
	err := r.pool.QueryRow(ctx, query, sensorID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("ReadingRepository.GetReadingCountToday: %w", err)
	}
	
	return count, nil
}

// Close closes the connection pool
func (r *PostgresReadingRepository) Close() {
	r.pool.Close()
}
