package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	geov1 "github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/soundmap/soundmap/services/geo-index/internal/cache"
	"github.com/soundmap/soundmap/services/geo-index/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// H3Util defines the interface for H3 geospatial operations
type H3Util interface {
	LatLngToH3Index(lat, lng float64, resolution int32) string
	CellsFromRadius(lat, lng float64, radiusKm float64, resolution int32) []string
	ValidateH3Index(h3Index string) bool
}

// GeoService implements the GeoIndexService gRPC handler
type GeoService struct {
	repo       repository.GeoRepository
	cache      cache.CellCache
	h3Util     H3Util
	logger     *slog.Logger
	meter      metric.Meter
	cacheExpiry time.Duration

	queriesTotal      metric.Int64Counter
	cacheHitsTotal    metric.Int64Counter
	cacheMissesTotal  metric.Int64Counter
	queryDurationMs   metric.Float64Histogram
}

// NewGeoService creates a new geo service
func NewGeoService(repo repository.GeoRepository, cache cache.CellCache, h3Util H3Util, logger *slog.Logger, meter metric.Meter, cacheExpiry time.Duration) (*GeoService, error) {
	svc := &GeoService{
		repo:        repo,
		cache:       cache,
		h3Util:      h3Util,
		logger:      logger,
		meter:       meter,
		cacheExpiry: cacheExpiry,
	}

	var err error

	svc.queriesTotal, err = meter.Int64Counter("geo_queries_total", metric.WithDescription("Total number of geo queries"))
	if err != nil {
		return nil, err
	}

	svc.cacheHitsTotal, err = meter.Int64Counter("geo_cache_hits_total", metric.WithDescription("Total number of cache hits"))
	if err != nil {
		return nil, err
	}

	svc.cacheMissesTotal, err = meter.Int64Counter("geo_cache_misses_total", metric.WithDescription("Total number of cache misses"))
	if err != nil {
		return nil, err
	}

	svc.queryDurationMs, err = meter.Float64Histogram("geo_query_duration_ms",
		metric.WithDescription("Query duration in milliseconds"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 25, 50, 100, 250, 500),
	)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// QueryHotspots returns noise hotspots within a radius
func (s *GeoService) QueryHotspots(ctx context.Context, req *geov1.QueryHotspotsRequest) (*geov1.QueryHotspotsResponse, error) {
	startTime := time.Now()

	// Validate coordinates
	if req.Center == nil {
		return nil, status.Error(codes.InvalidArgument, "center is required")
	}
	if req.Center.Latitude < -90 || req.Center.Latitude > 90 {
		return nil, status.Error(codes.InvalidArgument, "latitude must be between -90 and 90")
	}
	if req.Center.Longitude < -180 || req.Center.Longitude > 180 {
		return nil, status.Error(codes.InvalidArgument, "longitude must be between -180 and 180")
	}

	// Validate radius
	if req.RadiusKm < 0.1 || req.RadiusKm > 50 {
		return nil, status.Error(codes.InvalidArgument, "radius_km must be between 0.1 and 50")
	}

	// Validate resolution
	if req.Resolution < 5 || req.Resolution > 12 {
		return nil, status.Error(codes.InvalidArgument, "resolution must be between 5 and 12")
	}

	// Get H3 cells for the radius
	h3Indexes := s.h3Util.CellsFromRadius(req.Center.Latitude, req.Center.Longitude, req.RadiusKm, req.Resolution)
	if len(h3Indexes) == 0 {
		return &geov1.QueryHotspotsResponse{
			Cells:         []*geov1.HexCell{},
			Center:        req.Center,
			RadiusKm:      req.RadiusKm,
			QueriedAt:     timestamppb.Now(),
			QueryTimeMs:   float32(time.Since(startTime).Milliseconds()),
		}, nil
	}

	// Try cache first
	cachedCells, err := s.cache.GetCells(ctx, h3Indexes)
	if err != nil {
		// Cache error - log but continue (cache-aside pattern)
		s.logger.Warn("cache get error, falling through to DB", slog.String("error", err.Error()))
		cachedCells = make(map[string]*geov1.HexCell)
	}

	// Find which cells are missing from cache
	var missingIndexes []string
	for _, h3Index := range h3Indexes {
		if _, found := cachedCells[h3Index]; !found {
			missingIndexes = append(missingIndexes, h3Index)
		}
	}

	// Update metrics
	cacheHits := int64(len(cachedCells))
	cacheMisses := int64(len(missingIndexes))
	s.cacheHitsTotal.Add(ctx, cacheHits)
	s.cacheMissesTotal.Add(ctx, cacheMisses)

	// Query DB for missing cells
	var dbCells []*geov1.HexCell
	if len(missingIndexes) > 0 {
		dbCells, err = s.repo.GetHotspotsInCells(ctx, missingIndexes, req.MinDecibel)
		if err != nil {
			s.logger.Error("DB query error", slog.String("error", err.Error()))
			return nil, status.Errorf(codes.Internal, "failed to query hotspots: %v", err)
		}

		// Write DB results to cache asynchronously
		if len(dbCells) > 0 {
			go func() {
				cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				cellsToCache := make(map[string]*geov1.HexCell)
				for _, cell := range dbCells {
					cellsToCache[cell.H3Index] = cell
				}

				if err := s.cache.SetCells(cacheCtx, cellsToCache, s.cacheExpiry); err != nil {
					s.logger.Error("cache set error", slog.String("error", err.Error()))
				}
			}()
		}
	}

	// Merge cached and DB cells
	allCells := make(map[string]*geov1.HexCell)
	for _, cell := range cachedCells {
		allCells[cell.H3Index] = cell
	}
	for _, cell := range dbCells {
		allCells[cell.H3Index] = cell
	}

	// Filter by min_decibel and sort by avg_decibel descending
	var filteredCells []*geov1.HexCell
	for _, cell := range allCells {
		if cell.AvgDecibel >= req.MinDecibel {
			filteredCells = append(filteredCells, cell)
		}
	}

	sort.Slice(filteredCells, func(i, j int) bool {
		return filteredCells[i].AvgDecibel > filteredCells[j].AvgDecibel
	})

	// Apply limit
	if req.Limit > 0 && int(req.Limit) < len(filteredCells) {
		filteredCells = filteredCells[:req.Limit]
	}

	// Record metrics
	duration := float64(time.Since(startTime).Milliseconds())
	s.queryDurationMs.Record(ctx, duration)
	s.queriesTotal.Add(ctx, 1, metric.WithAttributes(metric.StringAttribute("method", "QueryHotspots")))

	return &geov1.QueryHotspotsResponse{
		Cells:       filteredCells,
		Center:      req.Center,
		RadiusKm:    req.RadiusKm,
		QueriedAt:   timestamppb.Now(),
		QueryTimeMs: float32(duration),
	}, nil
}

// GetHexCellStats returns detailed stats for a single H3 cell
func (s *GeoService) GetHexCellStats(ctx context.Context, req *geov1.GetHexCellStatsRequest) (*geov1.GetHexCellStatsResponse, error) {
	startTime := time.Now()

	// Validate h3_index
	if req.H3Index == "" {
		return nil, status.Error(codes.InvalidArgument, "h3_index is required")
	}
	if !s.h3Util.ValidateH3Index(req.H3Index) {
		return nil, status.Error(codes.InvalidArgument, "invalid h3_index")
	}

	// Validate time range
	if req.TimeRangeHours < 1 || req.TimeRangeHours > 168 {
		return nil, status.Error(codes.InvalidArgument, "time_range_hours must be between 1 and 168")
	}

	// Try cache first (only for basic stats, hourly breakdown is always from DB)
	var cell *geov1.HexCell
	cachedCell, err := s.cache.GetCell(ctx, req.H3Index)
	if err != nil {
		s.logger.Warn("cache get error", slog.String("error", err.Error()))
	} else if cachedCell != nil {
		s.cacheHitsTotal.Add(ctx, 1)
		cell = cachedCell
	} else {
		s.cacheMissesTotal.Add(ctx, 1)
	}

	// Query DB for stats and hourly breakdown
	cell, hourlyBreakdown, err := s.repo.GetHexCellStats(ctx, req.H3Index, int(req.TimeRangeHours))
	if err != nil {
		s.logger.Error("DB query error", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to get cell stats: %v", err)
	}

	// Cache the result for next time
	go func() {
		cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.cache.SetCell(cacheCtx, req.H3Index, cell, s.cacheExpiry); err != nil {
			s.logger.Error("cache set error", slog.String("error", err.Error()))
		}
	}()

	// Record metrics
	duration := float64(time.Since(startTime).Milliseconds())
	s.queryDurationMs.Record(ctx, duration)
	s.queriesTotal.Add(ctx, 1, metric.WithAttributes(metric.StringAttribute("method", "GetHexCellStats")))

	return &geov1.GetHexCellStatsResponse{
		Cell:            cell,
		HourlyBreakdown: hourlyBreakdown,
		QueryTimeMs:     float32(duration),
	}, nil
}

// StreamCellUpdates streams real-time updates for specified H3 cells
func (s *GeoService) StreamCellUpdates(req *geov1.StreamCellUpdatesRequest, stream geov1.GeoIndexService_StreamCellUpdatesServer) error {
	ctx := stream.Context()

	// Validate h3_indexes
	if len(req.H3Indexes) == 0 {
		return status.Error(codes.InvalidArgument, "h3_indexes must not be empty")
	}
	for _, h3Index := range req.H3Indexes {
		if !s.h3Util.ValidateH3Index(h3Index) {
			return status.Errorf(codes.InvalidArgument, "invalid h3_index: %s", h3Index)
		}
	}

	s.logger.Info("starting cell update stream", slog.Int("cell_count", len(req.H3Indexes)))

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("cell update stream cancelled")
			return nil

		case <-ticker.C:
			// Query latest data for cells
			cells, err := s.repo.GetLatestCellUpdate(ctx, req.H3Indexes)
			if err != nil {
				s.logger.Error("DB query error", slog.String("error", err.Error()))
				continue
			}

			// Send updates for each cell
			for _, cell := range cells {
				update := &geov1.CellUpdate{
					H3Index:   cell.H3Index,
					EventType: "UPDATE",
					UpdatedAt: timestamppb.Now(),
					Cell:      cell,
				}

				if err := stream.Send(update); err != nil {
					s.logger.Error("failed to send update", slog.String("error", err.Error()))
					return status.Errorf(codes.Internal, "failed to send update: %v", err)
				}
			}
		}
	}
}
