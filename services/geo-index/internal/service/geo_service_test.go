package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	geov1 "github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/soundmap/soundmap/services/geo-index/internal/cache"
	"github.com/soundmap/soundmap/services/geo-index/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"github.com/stretchr/testify/assert"
)

// MockGeoRepository for testing
type MockGeoRepository struct {
	GetHexCellStatsFunc     func(ctx context.Context, h3Index string, timeRangeHours int) (*geov1.HexCell, map[string]float64, error)
	GetHotspotsInCellsFunc  func(ctx context.Context, h3Indexes []string, minDecibel float64) ([]*geov1.HexCell, error)
	GetLatestCellUpdateFunc func(ctx context.Context, h3Indexes []string) ([]*geov1.HexCell, error)
}

func (m *MockGeoRepository) GetHexCellStats(ctx context.Context, h3Index string, timeRangeHours int) (*geov1.HexCell, map[string]float64, error) {
	if m.GetHexCellStatsFunc != nil {
		return m.GetHexCellStatsFunc(ctx, h3Index, timeRangeHours)
	}
	return nil, nil, nil
}

func (m *MockGeoRepository) GetHotspotsInCells(ctx context.Context, h3Indexes []string, minDecibel float64) ([]*geov1.HexCell, error) {
	if m.GetHotspotsInCellsFunc != nil {
		return m.GetHotspotsInCellsFunc(ctx, h3Indexes, minDecibel)
	}
	return nil, nil
}

func (m *MockGeoRepository) GetLatestCellUpdate(ctx context.Context, h3Indexes []string) ([]*geov1.HexCell, error) {
	if m.GetLatestCellUpdateFunc != nil {
		return m.GetLatestCellUpdateFunc(ctx, h3Indexes)
	}
	return nil, nil
}

// MockCellCache for testing
type MockCellCache struct {
	GetCellFunc    func(ctx context.Context, h3Index string) (*geov1.HexCell, error)
	SetCellFunc    func(ctx context.Context, h3Index string, cell *geov1.HexCell, expiry time.Duration) error
	GetCellsFunc   func(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error)
	SetCellsFunc   func(ctx context.Context, cells map[string]*geov1.HexCell, expiry time.Duration) error
}

func (m *MockCellCache) GetCell(ctx context.Context, h3Index string) (*geov1.HexCell, error) {
	if m.GetCellFunc != nil {
		return m.GetCellFunc(ctx, h3Index)
	}
	return nil, nil
}

func (m *MockCellCache) SetCell(ctx context.Context, h3Index string, cell *geov1.HexCell, expiry time.Duration) error {
	if m.SetCellFunc != nil {
		return m.SetCellFunc(ctx, h3Index, cell, expiry)
	}
	return nil
}

func (m *MockCellCache) GetCells(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error) {
	if m.GetCellsFunc != nil {
		return m.GetCellsFunc(ctx, h3Indexes)
	}
	return nil, nil
}

func (m *MockCellCache) SetCells(ctx context.Context, cells map[string]*geov1.HexCell, expiry time.Duration) error {
	if m.SetCellsFunc != nil {
		return m.SetCellsFunc(ctx, cells, expiry)
	}
	return nil
}

// MockH3Util for testing
type MockH3Util struct {
	LatLngToH3IndexFunc    func(lat, lng float64, resolution int32) string
	CellsFromRadiusFunc    func(lat, lng float64, radiusKm float64, resolution int32) []string
	ValidateH3IndexFunc  func(h3Index string) bool
}

func (m *MockH3Util) LatLngToH3Index(lat, lng float64, resolution int32) string {
	if m.LatLngToH3IndexFunc != nil {
		return m.LatLngToH3IndexFunc(lat, lng, resolution)
	}
	return "8928308280fffff"
}

func (m *MockH3Util) CellsFromRadius(lat, lng float64, radiusKm float64, resolution int32) []string {
	if m.CellsFromRadiusFunc != nil {
		return m.CellsFromRadiusFunc(lat, lng, radiusKm, resolution)
	}
	return []string{"8928308280fffff", "8928308281fffff"}
}

func (m *MockH3Util) ValidateH3Index(h3Index string) bool {
	if m.ValidateH3IndexFunc != nil {
		return m.ValidateH3IndexFunc(h3Index)
	}
	return len(h3Index) == 15
}

func createGeoService(repo repository.GeoRepository, cache cache.CellCache, h3Util H3Util) (*GeoService, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")
	return NewGeoService(repo, cache, h3Util, logger, meter, 30*time.Second)
}

func TestQueryHotspots_CacheHitAll(t *testing.T) {
	repo := &MockGeoRepository{}
	cache := &MockCellCache{
		GetCellsFunc: func(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error) {
			return map[string]*geov1.HexCell{
				"8928308280fffff": {H3Index: "8928308280fffff", AvgDecibel: 75.5, ReadingCount: 10},
				"8928308281fffff": {H3Index: "8928308281fffff", AvgDecibel: 80.2, ReadingCount: 15},
			}, nil
		},
	}
	h3Util := &MockH3Util{}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   5.0,
		Resolution: 9,
		MinDecibel: 60.0,
		Limit:      10,
	}

	resp, err := svc.QueryHotspots(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 2, len(resp.Cells))
	// Should be sorted by avg_decibel descending
	assert.Equal(t, 80.2, resp.Cells[0].AvgDecibel)
	assert.Equal(t, 75.5, resp.Cells[1].AvgDecibel)
}

func TestQueryHotspots_RadiusTooLarge(t *testing.T) {
	repo := &MockGeoRepository{}
	cache := &MockCellCache{}
	h3Util := &MockH3Util{}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   100.0, // Too large
		Resolution: 9,
	}

	_, err = svc.QueryHotspots(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "radius_km must be between")
}

func TestQueryHotspots_InvalidResolution(t *testing.T) {
	repo := &MockGeoRepository{}
	cache := &MockCellCache{}
	h3Util := &MockH3Util{}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   5.0,
		Resolution: 15, // Too high
	}

	_, err = svc.QueryHotspots(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "resolution must be between")
}

func TestQueryHotspots_PartialCacheMiss(t *testing.T) {
	repo := &MockGeoRepository{
		GetHotspotsInCellsFunc: func(ctx context.Context, h3Indexes []string, minDecibel float64) ([]*geov1.HexCell, error) {
			// Return data for missing cells
			return []*geov1.HexCell{
				{H3Index: "8928308281fffff", AvgDecibel: 80.2, ReadingCount: 15},
			}, nil
		},
	}
	cache := &MockCellCache{
		GetCellsFunc: func(ctx context.Context, h3Indexes []string) (map[string]*geov1.HexCell, error) {
			// Only return one cell from cache
			return map[string]*geov1.HexCell{
				"8928308280fffff": {H3Index: "8928308280fffff", AvgDecibel: 75.5, ReadingCount: 10},
			}, nil
		},
	}
	h3Util := &MockH3Util{
		CellsFromRadiusFunc: func(lat, lng float64, radiusKm float64, resolution int32) []string {
			return []string{"8928308280fffff", "8928308281fffff"}
		},
	}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   5.0,
		Resolution: 9,
		MinDecibel: 60.0,
	}

	resp, err := svc.QueryHotspots(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(resp.Cells))
}

func TestGetHexCellStats_InvalidH3Index(t *testing.T) {
	repo := &MockGeoRepository{}
	cache := &MockCellCache{}
	h3Util := &MockH3Util{
		ValidateH3IndexFunc: func(h3Index string) bool {
			return false
		},
	}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.GetHexCellStatsRequest{
		H3Index:          "invalid",
		TimeRangeHours:   24,
	}

	_, err = svc.GetHexCellStats(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetHexCellStats_TimeRangeTooLarge(t *testing.T) {
	repo := &MockGeoRepository{}
	cache := &MockCellCache{}
	h3Util := &MockH3Util{}
	svc, err := createGeoService(repo, cache, h3Util)
	assert.NoError(t, err)

	req := &geov1.GetHexCellStatsRequest{
		H3Index:          "8928308280fffff",
		TimeRangeHours:   200, // Too large (max 168)
	}

	_, err = svc.GetHexCellStats(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "time_range_hours must be between")
}
