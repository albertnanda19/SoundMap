package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FiredAlert represents an alert that has been triggered
type FiredAlert struct {
	AlertID          string
	ThresholdID      string
	SensorID         string
	H3Index          string
	Severity         string
	CurrentDecibel   float64
	ThresholdDecibel float64
	Message          string
	FiredAt          time.Time
	AcknowledgedAt   *time.Time
	AcknowledgedBy   string
}

// AlertRepository defines the interface for alert storage
type AlertRepository interface {
	SaveAlert(ctx context.Context, alert *FiredAlert) error
	AcknowledgeAlert(ctx context.Context, alertID, acknowledgedBy string) (*FiredAlert, error)
	GetUnacknowledgedAlerts(ctx context.Context) ([]*FiredAlert, error)
}

// PostgresAlertRepository implements AlertRepository using PostgreSQL
type PostgresAlertRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresAlertRepository creates a new repository
func NewPostgresAlertRepository(pool *pgxpool.Pool) *PostgresAlertRepository {
	return &PostgresAlertRepository{pool: pool}
}

// SaveAlert saves a fired alert to the database
func (r *PostgresAlertRepository) SaveAlert(ctx context.Context, alert *FiredAlert) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if alert.AlertID == "" {
		alert.AlertID = uuid.New().String()
	}

	query := `
		INSERT INTO fired_alerts (
			id, threshold_id, sensor_id, h3_index, severity,
			current_decibel, threshold_decibel, message, fired_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.pool.Exec(ctx, query,
		alert.AlertID,
		alert.ThresholdID,
		alert.SensorID,
		alert.H3Index,
		alert.Severity,
		alert.CurrentDecibel,
		alert.ThresholdDecibel,
		alert.Message,
		alert.FiredAt,
	)
	if err != nil {
		return fmt.Errorf("AlertRepository.SaveAlert: %w", err)
	}

	return nil
}

// AcknowledgeAlert marks an alert as acknowledged
func (r *PostgresAlertRepository) AcknowledgeAlert(ctx context.Context, alertID, acknowledgedBy string) (*FiredAlert, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	acknowledgedAt := time.Now()

	query := `
		UPDATE fired_alerts
		SET acknowledged_at = $1, acknowledged_by = $2
		WHERE id = $3
		RETURNING id, threshold_id, sensor_id, h3_index, severity,
			current_decibel, threshold_decibel, message, fired_at, acknowledged_at, acknowledged_by
	`

	var alert FiredAlert
	err := r.pool.QueryRow(ctx, query, acknowledgedAt, acknowledgedBy, alertID).Scan(
		&alert.AlertID,
		&alert.ThresholdID,
		&alert.SensorID,
		&alert.H3Index,
		&alert.Severity,
		&alert.CurrentDecibel,
		&alert.ThresholdDecibel,
		&alert.Message,
		&alert.FiredAt,
		&alert.AcknowledgedAt,
		&alert.AcknowledgedBy,
	)
	if err != nil {
		return nil, fmt.Errorf("AlertRepository.AcknowledgeAlert: %w", err)
	}

	return &alert, nil
}

// GetUnacknowledgedAlerts returns all unacknowledged alerts
func (r *PostgresAlertRepository) GetUnacknowledgedAlerts(ctx context.Context) ([]*FiredAlert, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, threshold_id, sensor_id, h3_index, severity,
			current_decibel, threshold_decibel, message, fired_at
		FROM fired_alerts
		WHERE acknowledged_at IS NULL
		ORDER BY fired_at DESC
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("AlertRepository.GetUnacknowledgedAlerts: %w", err)
	}
	defer rows.Close()

	var alerts []*FiredAlert
	for rows.Next() {
		var alert FiredAlert
		err := rows.Scan(
			&alert.AlertID,
			&alert.ThresholdID,
			&alert.SensorID,
			&alert.H3Index,
			&alert.Severity,
			&alert.CurrentDecibel,
			&alert.ThresholdDecibel,
			&alert.Message,
			&alert.FiredAt,
		)
		if err != nil {
			return nil, fmt.Errorf("AlertRepository.GetUnacknowledgedAlerts: scan error: %w", err)
		}
		alerts = append(alerts, &alert)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("AlertRepository.GetUnacknowledgedAlerts: row error: %w", err)
	}

	return alerts, nil
}

// Close closes the connection pool
func (r *PostgresAlertRepository) Close() {
	r.pool.Close()
}
