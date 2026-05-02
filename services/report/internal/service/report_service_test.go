package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	reportv1 "github.com/soundmap/soundmap/gen/go/report/v1"
	"github.com/soundmap/soundmap/services/report/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/stretchr/testify/assert"
)

// MockReportRepository for testing
type MockReportRepository struct {
	GetZoneStatsFunc      func(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error)
	GetCityZoneStatsFunc  func(ctx context.Context, start, end time.Time, resolution, limit int) ([]*reportv1.ZoneStats, error)
}

func (m *MockReportRepository) GetZoneStats(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error) {
	if m.GetZoneStatsFunc != nil {
		return m.GetZoneStatsFunc(ctx, h3Index, start, end)
	}
	return nil, nil
}

func (m *MockReportRepository) GetCityZoneStats(ctx context.Context, start, end time.Time, resolution, limit int) ([]*reportv1.ZoneStats, error) {
	if m.GetCityZoneStatsFunc != nil {
		return m.GetCityZoneStatsFunc(ctx, start, end, resolution, limit)
	}
	return nil, nil
}

func createReportService(repo repository.ReportRepository) (*ReportService, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")
	return NewReportService(repo, logger, meter)
}

func TestGenerateZoneReport_ValidRequest(t *testing.T) {
	startTime := time.Now().Add(-7 * 24 * time.Hour)
	endTime := time.Now()

	repo := &MockReportRepository{
		GetZoneStatsFunc: func(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error) {
			return &reportv1.ZoneStats{
				H3Index:         h3Index,
				AvgDecibel:      75.5,
				MaxDecibel:      95.2,
				MinDecibel:      45.1,
				P95Decibel:      85.0,
				ReadingCount:    1000,
				AlertCount:      15,
				WorstHour:       "14:00",
				LastUpdated:     timestamppb.Now(),
			}, nil
		},
	}
	svc, err := createReportService(repo)
	assert.NoError(t, err)

	req := &reportv1.GenerateZoneReportRequest{
		H3Index: "8928308280fffff",
		TimeRange: &reportv1.TimeRange{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
	}

	resp, err := svc.GenerateZoneReport(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.ReportId)
	assert.Equal(t, "8928308280fffff", resp.ZoneId)
	assert.NotNil(t, resp.Stats)
	assert.Equal(t, 75.5, resp.Stats.AvgDecibel)
	assert.Equal(t, 95.2, resp.Stats.MaxDecibel)

	// Verify recommendations based on the high values
	assert.Greater(t, len(resp.Recommendations), 0)
	containsCritical := false
	for _, rec := range resp.Recommendations {
		if contains(rec, "critical") || contains(rec, "Immediate") {
			containsCritical = true
			break
		}
	}
	assert.True(t, containsCritical, "Should contain critical level recommendation")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGenerateZoneReport_StartAfterEnd(t *testing.T) {
	startTime := time.Now()
	endTime := time.Now().Add(-7 * 24 * time.Hour) // End before start

	repo := &MockReportRepository{}
	svc, err := createReportService(repo)
	assert.NoError(t, err)

	req := &reportv1.GenerateZoneReportRequest{
		H3Index: "8928308280fffff",
		TimeRange: &reportv1.TimeRange{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
	}

	_, err = svc.GenerateZoneReport(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "start_time must be before end_time")
}

func TestGenerateZoneReport_RangeTooLarge(t *testing.T) {
	startTime := time.Now().Add(-100 * 24 * time.Hour) // 100 days ago
	endTime := time.Now()

	repo := &MockReportRepository{}
	svc, err := createReportService(repo)
	assert.NoError(t, err)

	req := &reportv1.GenerateZoneReportRequest{
		H3Index: "8928308280fffff",
		TimeRange: &reportv1.TimeRange{
			StartTime: timestamppb.New(startTime),
			EndTime:   timestamppb.New(endTime),
		},
	}

	_, err = svc.GenerateZoneReport(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "must not exceed 90 days")
}

func TestGenerateZoneReport_RepositoryError(t *testing.T) {
	repo := &MockReportRepository{
		GetZoneStatsFunc: func(ctx context.Context, h3Index string, start, end time.Time) (*reportv1.ZoneStats, error) {
			return nil, errors.New("database error")
		},
	}
	svc, err := createReportService(repo)
	assert.NoError(t, err)

	req := &reportv1.GenerateZoneReportRequest{
		H3Index: "8928308280fffff",
		TimeRange: &reportv1.TimeRange{
			StartTime: timestamppb.New(time.Now().Add(-7 * 24 * time.Hour)),
			EndTime:   timestamppb.Now(),
		},
	}

	_, err = svc.GenerateZoneReport(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGenerateRecommendations(t *testing.T) {
	tests := []struct {
		name     string
		stats    *reportv1.ZoneStats
		expected int // minimum number of expected recommendations
	}{
		{
			name: "critical levels",
			stats: &reportv1.ZoneStats{
				AvgDecibel: 75.0,
				MaxDecibel: 95.0,
				AlertCount: 15,
			},
			expected: 2,
		},
		{
			name: "moderate levels",
			stats: &reportv1.ZoneStats{
				AvgDecibel: 50.0,
				MaxDecibel: 70.0,
				AlertCount: 5,
			},
			expected: 1,
		},
		{
			name: "safe levels",
			stats: &reportv1.ZoneStats{
				AvgDecibel: 30.0,
				MaxDecibel: 50.0,
				AlertCount: 0,
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recommendations := generateRecommendations(tt.stats)
			assert.GreaterOrEqual(t, len(recommendations), tt.expected)
		})
	}
}
