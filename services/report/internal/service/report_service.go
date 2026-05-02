package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	reportv1 "github.com/soundmap/soundmap/gen/go/report/v1"
	"github.com/soundmap/soundmap/services/report/internal/repository"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReportService implements the ReportService gRPC handler
type ReportService struct {
	repo     repository.ReportRepository
	logger   *slog.Logger
	meter    metric.Meter

	reportsGeneratedTotal metric.Int64Counter
	generationDurationMs    metric.Float64Histogram
}

// NewReportService creates a new report service
func NewReportService(repo repository.ReportRepository, logger *slog.Logger, meter metric.Meter) (*ReportService, error) {
	svc := &ReportService{
		repo:   repo,
		logger: logger,
		meter:  meter,
	}

	var err error

	svc.reportsGeneratedTotal, err = meter.Int64Counter("report_generated_total", metric.WithDescription("Total number of reports generated"))
	if err != nil {
		return nil, err
	}

	svc.generationDurationMs, err = meter.Float64Histogram("report_generation_duration_ms",
		metric.WithDescription("Report generation duration in milliseconds"),
		metric.WithExplicitBucketBoundaries(10, 50, 100, 250, 500, 1000, 2500, 5000),
	)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// GenerateZoneReport generates a report for a specific zone
func (s *ReportService) GenerateZoneReport(ctx context.Context, req *reportv1.GenerateZoneReportRequest) (*reportv1.GenerateZoneReportResponse, error) {
	startTime := time.Now()

	// Validate h3_index
	if req.H3Index == "" {
		return nil, status.Error(codes.InvalidArgument, "h3_index is required")
	}

	// Validate time range
	startTimeReq := req.TimeRange.StartTime.AsTime()
	endTimeReq := req.TimeRange.EndTime.AsTime()

	if startTimeReq.After(endTimeReq) {
		return nil, status.Error(codes.InvalidArgument, "start_time must be before end_time")
	}

	maxRange := 90 * 24 * time.Hour // 90 days
	if endTimeReq.Sub(startTimeReq) > maxRange {
		return nil, status.Error(codes.InvalidArgument, "time range must not exceed 90 days")
	}

	// Query zone stats
	stats, err := s.repo.GetZoneStats(ctx, req.H3Index, startTimeReq, endTimeReq)
	if err != nil {
		s.logger.Error("failed to get zone stats", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to generate zone report: %v", err)
	}

	// Generate recommendations
	recommendations := generateRecommendations(stats)

	// Record metrics
	duration := float64(time.Since(startTime).Milliseconds())
	s.generationDurationMs.Record(ctx, duration)
	s.reportsGeneratedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "zone")))

	s.logger.Info("zone report generated",
		slog.String("report_id", stats.H3Index),
		slog.Float64("avg_decibel", stats.AvgDecibel))

	return &reportv1.GenerateZoneReportResponse{
		ReportId:        uuid.New().String(),
		ZoneId:          req.H3Index,
		Stats:           stats,
		Recommendations: recommendations,
		GeneratedAt:     timestamppb.Now(),
	}, nil
}

// GenerateCityReport generates a streaming report for an entire city
func (s *ReportService) GenerateCityReport(req *reportv1.GenerateCityReportRequest, stream reportv1.ReportService_GenerateCityReportServer) error {
	ctx := stream.Context()
	startTime := time.Now()

	// Validate city_name
	if req.CityName == "" {
		return status.Error(codes.InvalidArgument, "city_name is required")
	}

	// Validate time range
	startTimeReq := req.TimeRange.StartTime.AsTime()
	endTimeReq := req.TimeRange.EndTime.AsTime()

	if startTimeReq.After(endTimeReq) {
		return status.Error(codes.InvalidArgument, "start_time must be before end_time")
	}

	// Validate top_n_hotspots
	if req.TopNHotspots < 1 || req.TopNHotspots > 100 {
		return status.Error(codes.InvalidArgument, "top_n_hotspots must be between 1 and 100")
	}

	s.logger.Info("generating city report",
		slog.String("city", req.CityName),
		slog.Int("top_n", int(req.TopNHotspots)))

	// Query zone stats for the city
	statsList, err := s.repo.GetCityZoneStats(ctx, startTimeReq, endTimeReq, int(req.Resolution), int(req.TopNHotspots))
	if err != nil {
		s.logger.Error("failed to get city zone stats", slog.String("error", err.Error()))
		return status.Errorf(codes.Internal, "failed to generate city report: %v", err)
	}

	// Generate report ID (same for all chunks)
	reportID := uuid.New().String()

	// Chunk the results (10 zones per chunk)
	chunkSize := 10
	totalChunks := (len(statsList) + chunkSize - 1) / chunkSize

	for chunkIdx := 0; chunkIdx < totalChunks; chunkIdx++ {
		startIdx := chunkIdx * chunkSize
		endIdx := startIdx + chunkSize
		if endIdx > len(statsList) {
			endIdx = len(statsList)
		}

		chunkStats := statsList[startIdx:endIdx]

		// Add recommendations to each zone stat
		var zoneStatsWithRecommendations []*reportv1.ZoneStatsWithRecommendations
		for _, stat := range chunkStats {
			recs := generateRecommendations(stat)
			zoneStatsWithRecommendations = append(zoneStatsWithRecommendations, &reportv1.ZoneStatsWithRecommendations{
				Stats:           stat,
				Recommendations: recs,
			})
		}

		chunk := &reportv1.GenerateCityReportChunk{
			ChunkIndex: int32(chunkIdx + 1),
			TotalChunks: int32(totalChunks),
			ZoneStats:   statsList,
			ReportId:    reportID,
			CityName:    req.CityName,
			IsLastChunk: chunkIdx == totalChunks-1,
		}

		if err := stream.Send(chunk); err != nil {
			s.logger.Error("failed to send chunk",
				slog.Int("chunk", chunkIdx),
				slog.String("error", err.Error()))
			return status.Errorf(codes.Internal, "failed to send chunk: %v", err)
		}

		s.logger.Debug("streaming chunk",
			slog.Int("chunk", chunkIdx+1),
			slog.Int("of", totalChunks))

		// Artificial delay between chunks
		if chunkIdx < totalChunks-1 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Record metrics
	duration := float64(time.Since(startTime).Milliseconds())
	s.generationDurationMs.Record(ctx, duration)
	s.reportsGeneratedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("type", "city")))

	s.logger.Info("city report generated",
		slog.String("report_id", reportID),
		slog.String("city", req.CityName),
		slog.Int("zones", len(statsList)))

	return nil
}

// generateRecommendations creates recommendations based on zone statistics
func generateRecommendations(stats *reportv1.ZoneStats) []string {
	var recommendations []string

	// Recommendation based on average decibel
	if stats.AvgDecibel > 70 {
		recommendations = append(recommendations, "Consider sound barriers or vegetation buffers to reduce ambient noise levels")
	}

	// Recommendation based on maximum decibel
	if stats.MaxDecibel > 90 {
		recommendations = append(recommendations, "Immediate review recommended — critical noise levels detected")
	}

	// Recommendation based on alert count
	if stats.AlertCount > 10 {
		recommendations = append(recommendations, "High alert frequency — review threshold calibration and sensor placement")
	}

	// Recommendation based on health risk level
	switch stats.HealthRiskLevel {
	case commonv1.HealthRiskLevel_HEALTH_RISK_HIGH:
		recommendations = append(recommendations, "Health risk level is high — consider noise mitigation measures")
	case commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL:
		recommendations = append(recommendations, "Critical health risk — immediate action required for public safety")
	}

	// If no specific recommendations, add a general one
	if len(recommendations) == 0 {
		recommendations = append(recommendations, "Noise levels are within acceptable ranges — continue routine monitoring")
	}

	return recommendations
}
