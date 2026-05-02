package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalysisRecord represents an analysis result stored in the database
type AnalysisRecord struct {
	ID                 string
	ReadingID          string
	SensorID           string
	NoiseCategory      string
	HealthRiskLevel    string
	RiskScore          float64
	DominantFrequencyHz float64
	FrequencyBands     map[string]float64
	AnalyzedAt         time.Time
	H3Index            string
	H3Resolution       int32
}

// AnalysisRepository defines the interface for storing analysis results
type AnalysisRepository interface {
	SaveAnalysis(ctx context.Context, result *AnalysisRecord) error
	SaveAnalysisBatch(ctx context.Context, results []*AnalysisRecord) error
	GetLatestByH3Index(ctx context.Context, h3Index string, limit int) ([]*AnalysisRecord, error)
}

// PostgresAnalysisRepository implements AnalysisRepository using PostgreSQL
type PostgresAnalysisRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresAnalysisRepository creates a new repository with connection pooling
func NewPostgresAnalysisRepository(ctx context.Context, databaseURL string) (*PostgresAnalysisRepository, error) {
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

	return &PostgresAnalysisRepository{pool: pool}, nil
}

// SaveAnalysis stores a single analysis result
func (r *PostgresAnalysisRepository) SaveAnalysis(ctx context.Context, result *AnalysisRecord) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if result.ID == "" {
		result.ID = uuid.New().String()
	}

	query := `
		INSERT INTO analysis_results (
			id, reading_id, sensor_id, noise_category, health_risk_level, 
			risk_score, dominant_frequency_hz, analyzed_at, h3_index, h3_resolution
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err := r.pool.Exec(ctx, query,
		result.ID,
		result.ReadingID,
		result.SensorID,
		result.NoiseCategory,
		result.HealthRiskLevel,
		result.RiskScore,
		result.DominantFrequencyHz,
		result.AnalyzedAt,
		result.H3Index,
		result.H3Resolution,
	)
	if err != nil {
		return fmt.Errorf("AnalysisRepository.SaveAnalysis: %w", err)
	}

	return nil
}

// SaveAnalysisBatch saves multiple analysis results in a batch
func (r *PostgresAnalysisRepository) SaveAnalysisBatch(ctx context.Context, results []*AnalysisRecord) error {
	if len(results) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Begin transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("AnalysisRepository.SaveAnalysisBatch: failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		INSERT INTO analysis_results (
			id, reading_id, sensor_id, noise_category, health_risk_level, 
			risk_score, dominant_frequency_hz, analyzed_at, h3_index, h3_resolution
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	for _, result := range results {
		if result.ID == "" {
			result.ID = uuid.New().String()
		}

		_, err := tx.Exec(ctx, query,
			result.ID,
			result.ReadingID,
			result.SensorID,
			result.NoiseCategory,
			result.HealthRiskLevel,
			result.RiskScore,
			result.DominantFrequencyHz,
			result.AnalyzedAt,
			result.H3Index,
			result.H3Resolution,
		)
		if err != nil {
			return fmt.Errorf("AnalysisRepository.SaveAnalysisBatch: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("AnalysisRepository.SaveAnalysisBatch: failed to commit: %w", err)
	}

	return nil
}

// GetLatestByH3Index retrieves the latest analysis records for an H3 cell
func (r *PostgresAnalysisRepository) GetLatestByH3Index(ctx context.Context, h3Index string, limit int) ([]*AnalysisRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT id, reading_id, sensor_id, noise_category, health_risk_level,
			risk_score, dominant_frequency_hz, analyzed_at, h3_index, h3_resolution
		FROM analysis_results
		WHERE h3_index = $1
		ORDER BY analyzed_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, h3Index, limit)
	if err != nil {
		return nil, fmt.Errorf("AnalysisRepository.GetLatestByH3Index: %w", err)
	}
	defer rows.Close()

	var results []*AnalysisRecord
	for rows.Next() {
		var rec AnalysisRecord
		err := rows.Scan(
			&rec.ID,
			&rec.ReadingID,
			&rec.SensorID,
			&rec.NoiseCategory,
			&rec.HealthRiskLevel,
			&rec.RiskScore,
			&rec.DominantFrequencyHz,
			&rec.AnalyzedAt,
			&rec.H3Index,
			&rec.H3Resolution,
		)
		if err != nil {
			return nil, fmt.Errorf("AnalysisRepository.GetLatestByH3Index: scan error: %w", err)
		}
		results = append(results, &rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("AnalysisRepository.GetLatestByH3Index: row error: %w", err)
	}

	return results, nil
}

// Close closes the connection pool
func (r *PostgresAnalysisRepository) Close() {
	r.pool.Close()
}
