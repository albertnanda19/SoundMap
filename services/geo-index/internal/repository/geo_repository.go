package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	geov1 "github.com/soundmap/soundmap/gen/go/geo/v1"
)

// GeoRepository defines the interface for geospatial data storage
type GeoRepository interface {
	GetHexCellStats(ctx context.Context, h3Index string, timeRangeHours int) (*geov1.HexCell, map[string]float64, error)
	GetHotspotsInCells(ctx context.Context, h3Indexes []string, minDecibel float64) ([]*geov1.HexCell, error)
	GetLatestCellUpdate(ctx context.Context, h3Indexes []string) ([]*geov1.HexCell, error)
}

// PostgresGeoRepository implements GeoRepository using PostgreSQL/TimescaleDB
type PostgresGeoRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresGeoRepository creates a new repository with connection pooling
func NewPostgresGeoRepository(pool *pgxpool.Pool) *PostgresGeoRepository {
	return &PostgresGeoRepository{pool: pool}
}

// GetHexCellStats retrieves aggregated stats for a single H3 cell with hourly breakdown
func (r *PostgresGeoRepository) GetHexCellStats(ctx context.Context, h3Index string, timeRangeHours int) (*geov1.HexCell, map[string]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Query for aggregated stats
	statsQuery := `
		SELECT 
			AVG(risk_score) as avg_decibel,
			MAX(risk_score) as max_decibel,
			MIN(risk_score) as min_decibel,
			COUNT(*) as reading_count,
			MODE() WITHIN GROUP (ORDER BY noise_category) as dominant_category,
			MAX(analyzed_at) as last_updated
		FROM analysis_results
		WHERE h3_index = $1 AND analyzed_at >= NOW() - INTERVAL '1 hour' * $2
	`

	var cell geov1.HexCell
	var dominantCategory string
	err := r.pool.QueryRow(ctx, statsQuery, h3Index, timeRangeHours).Scan(
		&cell.AvgDecibel,
		&cell.MaxDecibel,
		&cell.MinDecibel,
		&cell.ReadingCount,
		&dominantCategory,
		&cell.LastUpdated,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("GeoRepository.GetHexCellStats: stats query error: %w", err)
	}

	cell.H3Index = h3Index

	// Query for hourly breakdown
	hourlyQuery := `
		SELECT 
			time_bucket('1 hour', analyzed_at) as hour,
			AVG(risk_score) as avg_score
		FROM analysis_results
		WHERE h3_index = $1 AND analyzed_at >= NOW() - INTERVAL '1 hour' * $2
		GROUP BY hour
		ORDER BY hour DESC
	`

	rows, err := r.pool.Query(ctx, hourlyQuery, h3Index, timeRangeHours)
	if err != nil {
		return nil, nil, fmt.Errorf("GeoRepository.GetHexCellStats: hourly query error: %w", err)
	}
	defer rows.Close()

	hourlyBreakdown := make(map[string]float64)
	for rows.Next() {
		var hour time.Time
		var avgScore float64
		if err := rows.Scan(&hour, &avgScore); err != nil {
			continue
		}
		hourlyBreakdown[hour.Format("15:04")] = avgScore
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("GeoRepository.GetHexCellStats: hourly rows error: %w", err)
	}

	return &cell, hourlyBreakdown, nil
}

// GetHotspotsInCells returns aggregated stats for cells exceeding minDecibel
func (r *PostgresGeoRepository) GetHotspotsInCells(ctx context.Context, h3Indexes []string, minDecibel float64) ([]*geov1.HexCell, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if len(h3Indexes) == 0 {
		return nil, nil
	}

	// Convert slice to array literal for SQL
	query := `
		SELECT 
			h3_index,
			AVG(risk_score) as avg_decibel,
			MAX(risk_score) as max_decibel,
			MIN(risk_score) as min_decibel,
			COUNT(*) as reading_count,
			MODE() WITHIN GROUP (ORDER BY noise_category) as dominant_category,
			MAX(analyzed_at) as last_updated
		FROM analysis_results
		WHERE h3_index = ANY($1)
			AND analyzed_at >= NOW() - INTERVAL '24 hours'
		GROUP BY h3_index
		HAVING AVG(risk_score) >= $2
		ORDER BY avg_decibel DESC
	`

	rows, err := r.pool.Query(ctx, query, h3Indexes, minDecibel)
	if err != nil {
		return nil, fmt.Errorf("GeoRepository.GetHotspotsInCells: %w", err)
	}
	defer rows.Close()

	var cells []*geov1.HexCell
	for rows.Next() {
		var cell geov1.HexCell
		var dominantCategory string

		err := rows.Scan(
			&cell.H3Index,
			&cell.AvgDecibel,
			&cell.MaxDecibel,
			&cell.MinDecibel,
			&cell.ReadingCount,
			&dominantCategory,
			&cell.LastUpdated,
		)
		if err != nil {
			continue
		}
		cells = append(cells, &cell)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GeoRepository.GetHotspotsInCells: row error: %w", err)
	}

	return cells, nil
}

// GetLatestCellUpdate retrieves the most recent analysis for each cell
func (r *PostgresGeoRepository) GetLatestCellUpdate(ctx context.Context, h3Indexes []string) ([]*geov1.HexCell, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if len(h3Indexes) == 0 {
		return nil, nil
	}

	query := `
		SELECT DISTINCT ON (h3_index)
			h3_index,
			risk_score as avg_decibel,
			risk_score as max_decibel,
			risk_score as min_decibel,
			1 as reading_count,
			noise_category as dominant_category,
			analyzed_at as last_updated
		FROM analysis_results
		WHERE h3_index = ANY($1)
		ORDER BY h3_index, analyzed_at DESC
	`

	rows, err := r.pool.Query(ctx, query, h3Indexes)
	if err != nil {
		return nil, fmt.Errorf("GeoRepository.GetLatestCellUpdate: %w", err)
	}
	defer rows.Close()

	var cells []*geov1.HexCell
	for rows.Next() {
		var cell geov1.HexCell
		var dominantCategory string

		err := rows.Scan(
			&cell.H3Index,
			&cell.AvgDecibel,
			&cell.MaxDecibel,
			&cell.MinDecibel,
			&cell.ReadingCount,
			&dominantCategory,
			&cell.LastUpdated,
		)
		if err != nil {
			continue
		}
		cells = append(cells, &cell)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GeoRepository.GetLatestCellUpdate: row error: %w", err)
	}

	return cells, nil
}

// Close closes the connection pool
func (r *PostgresGeoRepository) Close() {
	r.pool.Close()
}
