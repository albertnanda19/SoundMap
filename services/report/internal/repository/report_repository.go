package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	reportv1 "github.com/soundmap/soundmap/gen/go/report/v1"
)

// ReportRepository defines the interface for report data storage
type ReportRepository interface {
	GetZoneStats(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error)
	GetCityZoneStats(ctx context.Context, start, end time.Time, resolution, limit int) ([]*reportv1.ZoneStats, error)
}

// PostgresReportRepository implements ReportRepository using PostgreSQL
type PostgresReportRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresReportRepository creates a new repository
func NewPostgresReportRepository(pool *pgxpool.Pool) *PostgresReportRepository {
	return &PostgresReportRepository{pool: pool}
}

// GetZoneStats retrieves detailed statistics for a single zone (H3 cell)
func (r *PostgresReportRepository) GetZoneStats(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Query for zone statistics
	statsQuery := `
		SELECT 
			AVG(risk_score) as avg_decibel,
			MAX(risk_score) as max_decibel,
			MIN(risk_score) as min_decibel,
			PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY risk_score) as p95_decibel,
			COUNT(*) as reading_count,
			MODE() WITHIN GROUP (ORDER BY noise_category) as dominant_category,
			COUNT(DISTINCT date_trunc('day', analyzed_at)) as alert_count,
			MAX(analyzed_at) as last_updated
		FROM analysis_results
		WHERE h3_index = $1 AND analyzed_at BETWEEN $2 AND $3
	`

	var stats reportv1.ZoneStats
	var dominantCategory string
	err := r.pool.QueryRow(ctx, statsQuery, h3Index, start, end).Scan(
		&stats.AvgDecibel,
		&stats.MaxDecibel,
		&stats.MinDecibel,
		&stats.P95Decibel,
		&stats.ReadingCount,
		&dominantCategory,
		&stats.AlertCount,
		&stats.LastUpdated,
	)
	if err != nil {
		return nil, fmt.Errorf("ReportRepository.GetZoneStats: %w", err)
	}

	stats.H3Index = h3Index

	// Query for worst hour
	worstHourQuery := `
		SELECT 
			time_bucket('1 hour', analyzed_at) as hour,
			AVG(risk_score) as avg_score
		FROM analysis_results
		WHERE h3_index = $1 AND analyzed_at BETWEEN $2 AND $3
		GROUP BY hour
		ORDER BY avg_score DESC
		LIMIT 1
	`

	var worstHour time.Time
	var worstHourScore float64
	err = r.pool.QueryRow(ctx, worstHourQuery, h3Index, start, end).Scan(&worstHour, &worstHourScore)
	if err == nil {
		stats.WorstHour = worstHour.Format("15:04")
	}

	// Calculate health risk level based on P95
	stats.HealthRiskLevel = calculateHealthRiskLevel(stats.P95Decibel)

	return &stats, nil
}

// GetCityZoneStats retrieves aggregated stats for top N zones in a city
func (r *PostgresReportRepository) GetCityZoneStats(ctx context.Context, start, end time.Time, resolution, limit int) ([]*reportv1.ZoneStats, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	query := `
		SELECT 
			h3_index,
			AVG(risk_score) as avg_decibel,
			MAX(risk_score) as max_decibel,
			MIN(risk_score) as min_decibel,
			PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY risk_score) as p95_decibel,
			COUNT(*) as reading_count,
			MODE() WITHIN GROUP (ORDER BY noise_category) as dominant_category,
			COUNT(DISTINCT date_trunc('day', analyzed_at)) as alert_count,
			MAX(analyzed_at) as last_updated
		FROM analysis_results
		WHERE h3_index IS NOT NULL AND analyzed_at BETWEEN $1 AND $2
		GROUP BY h3_index
		ORDER BY avg_decibel DESC
		LIMIT $3
	`

	rows, err := r.pool.Query(ctx, query, start, end, limit)
	if err != nil {
		return nil, fmt.Errorf("ReportRepository.GetCityZoneStats: %w", err)
	}
	defer rows.Close()

	var statsList []*reportv1.ZoneStats
	for rows.Next() {
		var stats reportv1.ZoneStats
		var dominantCategory string

		err := rows.Scan(
			&stats.H3Index,
			&stats.AvgDecibel,
			&stats.MaxDecibel,
			&stats.MinDecibel,
			&stats.P95Decibel,
			&stats.ReadingCount,
			&dominantCategory,
			&stats.AlertCount,
			&stats.LastUpdated,
		)
		if err != nil {
			continue
		}

		stats.HealthRiskLevel = calculateHealthRiskLevel(stats.P95Decibel)
		statsList = append(statsList, &stats)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ReportRepository.GetCityZoneStats: row error: %w", err)
	}

	return statsList, nil
}

// Close closes the connection pool
func (r *PostgresReportRepository) Close() {
	r.pool.Close()
}

// calculateHealthRiskLevel determines risk level from P95 decibel level
func calculateHealthRiskLevel(p95Decibel float64) commonv1.HealthRiskLevel {
	switch {
	case p95Decibel < 25:
		return commonv1.HealthRiskLevel_HEALTH_RISK_SAFE
	case p95Decibel < 50:
		return commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE
	case p95Decibel < 75:
		return commonv1.HealthRiskLevel_HEALTH_RISK_HIGH
	default:
		return commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL
	}
}
