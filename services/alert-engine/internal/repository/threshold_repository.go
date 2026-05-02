package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	alertv1 "github.com/soundmap/soundmap/gen/go/alert/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ThresholdRepository defines the interface for threshold storage
type ThresholdRepository interface {
	CreateThreshold(ctx context.Context, t *alertv1.Threshold) error
	ListThresholds(ctx context.Context, page, pageSize int) ([]*alertv1.Threshold, int64, error)
	GetThresholdByID(ctx context.Context, id string) (*alertv1.Threshold, error)
	GetActiveThresholds(ctx context.Context) ([]*alertv1.Threshold, error)
}

// PostgresThresholdRepository implements ThresholdRepository using PostgreSQL
type PostgresThresholdRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresThresholdRepository creates a new repository
func NewPostgresThresholdRepository(ctx context.Context, databaseURL string) (*PostgresThresholdRepository, error) {
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

	return &PostgresThresholdRepository{pool: pool}, nil
}

// CreateThreshold saves a new threshold to the database
func (r *PostgresThresholdRepository) CreateThreshold(ctx context.Context, t *alertv1.Threshold) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if t.ThresholdId == "" {
		t.ThresholdId = uuid.New().String()
	}

	query := `
		INSERT INTO alert_thresholds (id, name, h3_index, max_decibel, severity, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	createdAt := t.CreatedAt.AsTime()
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	_, err := r.pool.Exec(ctx, query,
		t.ThresholdId,
		t.Name,
		t.H3Index,
		t.MaxDecibel,
		t.Severity.String(),
		t.IsActive,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("ThresholdRepository.CreateThreshold: %w", err)
	}

	return nil
}

// ListThresholds returns paginated thresholds
func (r *PostgresThresholdRepository) ListThresholds(ctx context.Context, page, pageSize int) ([]*alertv1.Threshold, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize

	// Get total count
	var total int64
	countQuery := `SELECT COUNT(*) FROM alert_thresholds`
	if err := r.pool.QueryRow(ctx, countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ThresholdRepository.ListThresholds: count error: %w", err)
	}

	// Get paginated results
	query := `
		SELECT id, name, h3_index, max_decibel, severity, is_active, created_at
		FROM alert_thresholds
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.pool.Query(ctx, query, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("ThresholdRepository.ListThresholds: query error: %w", err)
	}
	defer rows.Close()

	var thresholds []*alertv1.Threshold
	for rows.Next() {
		var t alertv1.Threshold
		var createdAt time.Time
		var severity string

		err := rows.Scan(
			&t.ThresholdId,
			&t.Name,
			&t.H3Index,
			&t.MaxDecibel,
			&severity,
			&t.IsActive,
			&createdAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("ThresholdRepository.ListThresholds: scan error: %w", err)
		}

		t.Severity = parseSeverity(severity)
		t.CreatedAt = timestamppb.New(createdAt)
		thresholds = append(thresholds, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ThresholdRepository.ListThresholds: row error: %w", err)
	}

	return thresholds, total, nil
}

// GetThresholdByID retrieves a single threshold by ID
func (r *PostgresThresholdRepository) GetThresholdByID(ctx context.Context, id string) (*alertv1.Threshold, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, name, h3_index, max_decibel, severity, is_active, created_at
		FROM alert_thresholds
		WHERE id = $1
	`

	var t alertv1.Threshold
	var createdAt time.Time
	var severity string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&t.ThresholdId,
		&t.Name,
		&t.H3Index,
		&t.MaxDecibel,
		&severity,
		&t.IsActive,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("ThresholdRepository.GetThresholdByID: %w", err)
	}

	t.Severity = parseSeverity(severity)
	t.CreatedAt = timestamppb.New(createdAt)

	return &t, nil
}

// GetActiveThresholds returns all active thresholds
func (r *PostgresThresholdRepository) GetActiveThresholds(ctx context.Context) ([]*alertv1.Threshold, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := `
		SELECT id, name, h3_index, max_decibel, severity, is_active, created_at
		FROM alert_thresholds
		WHERE is_active = true
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("ThresholdRepository.GetActiveThresholds: %w", err)
	}
	defer rows.Close()

	var thresholds []*alertv1.Threshold
	for rows.Next() {
		var t alertv1.Threshold
		var createdAt time.Time
		var severity string

		err := rows.Scan(
			&t.ThresholdId,
			&t.Name,
			&t.H3Index,
			&t.MaxDecibel,
			&severity,
			&t.IsActive,
			&createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("ThresholdRepository.GetActiveThresholds: scan error: %w", err)
		}

		t.Severity = parseSeverity(severity)
		t.CreatedAt = timestamppb.New(createdAt)
		thresholds = append(thresholds, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ThresholdRepository.GetActiveThresholds: row error: %w", err)
	}

	return thresholds, nil
}

// Close closes the connection pool
func (r *PostgresThresholdRepository) Close() {
	r.pool.Close()
}

// parseSeverity converts string to enum
func parseSeverity(s string) alertv1.AlertSeverity {
	switch s {
	case "ALERT_SEVERITY_INFO":
		return alertv1.AlertSeverity_ALERT_SEVERITY_INFO
	case "ALERT_SEVERITY_WARNING":
		return alertv1.AlertSeverity_ALERT_SEVERITY_WARNING
	case "ALERT_SEVERITY_CRITICAL":
		return alertv1.AlertSeverity_ALERT_SEVERITY_CRITICAL
	default:
		return alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED
	}
}
