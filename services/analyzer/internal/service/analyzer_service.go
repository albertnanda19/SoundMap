package service

import (
	"context"
	"io"
	"log/slog"
	"math"
	"time"

	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	analyzerv1 "github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/services/analyzer/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DSPAnalyzer defines the interface for DSP analysis operations
type DSPAnalyzer interface {
	ComputeFFT(samples []float64) []float64
	FrequencyBands(fft []float64, sampleRateHz int32) map[string]float64
	DominantFrequency(fft []float64, sampleRateHz int32) float64
	Analyze(decibelLevel, dominantFreq float64, bands map[string]float64) *AnalysisResult
}

// AnalysisResult holds the outcome of DSP analysis
type AnalysisResult struct {
	NoiseCategory       commonv1.NoiseCategory
	HealthRiskLevel     commonv1.HealthRiskLevel
	RiskScore           float64
	DominantFrequencyHz float64
	FrequencyBands      map[string]float64
}

// H3Util defines the interface for H3 geospatial operations
type H3Util interface {
	LatLngToH3Index(lat, lng float64, resolution int32) string
}

// AnalyzerService implements the analyzer gRPC service
type AnalyzerService struct {
	repo      repository.AnalysisRepository
	dsp       DSPAnalyzer
	h3Util    H3Util
	logger    *slog.Logger
	meter     metric.Meter

	// metrics
	readingsProcessed metric.Int64Counter
	processingDuration  metric.Float64Histogram
	activeBidiStreams metric.Int64UpDownCounter
}

// NewAnalyzerService creates a new analyzer service
func NewAnalyzerService(repo repository.AnalysisRepository, dsp DSPAnalyzer, h3Util H3Util, logger *slog.Logger, meter metric.Meter) (*AnalyzerService, error) {
	svc := &AnalyzerService{
		repo:     repo,
		dsp:      dsp,
		h3Util:   h3Util,
		logger:   logger,
		meter:    meter,
	}

	var err error

	// Register metrics
	svc.readingsProcessed, err = meter.Int64Counter("analyzer_readings_processed_total", metric.WithDescription("Total number of readings processed"))
	if err != nil {
		return nil, err
	}

	svc.processingDuration, err = meter.Float64Histogram("analyzer_processing_duration_ms",
		metric.WithDescription("Processing duration in milliseconds"),
		metric.WithExplicitBucketBoundaries(1, 5, 10, 25, 50, 100, 250, 500),
	)
	if err != nil {
		return nil, err
	}

	svc.activeBidiStreams, err = meter.Int64UpDownCounter("analyzer_active_bidi_streams", metric.WithDescription("Current number of active bidirectional streams"))
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// AnalyzeStream handles bidirectional streaming analysis
func (s *AnalyzerService) AnalyzeStream(stream analyzerv1.AnalyzerService_AnalyzeStreamServer) error {
	ctx := stream.Context()

	// Track active stream
	s.activeBidiStreams.Add(ctx, 1)
	defer s.activeBidiStreams.Add(ctx, -1)

	s.logger.Info("bidi stream opened")

	for {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "client disconnected")
		default:
		}

		req, err := stream.Recv()
		if err == io.EOF {
			s.logger.Info("bidi stream closed (EOF)")
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to receive: %v", err)
		}

		// Start timing
		startTime := time.Now()

		// Run DSP analysis
		result := s.analyzeRequest(req)

		// Record duration
		duration := float64(time.Since(startTime).Milliseconds())
		s.processingDuration.Record(ctx, duration)

		// Build response
		resp := &analyzerv1.AnalyzeResponse{
			ReadingId:           req.ReadingId,
			NoiseCategory:       result.NoiseCategory,
			HealthRiskLevel:     result.HealthRiskLevel,
			RiskScore:           result.RiskScore,
			DominantFrequencyHz: result.DominantFrequencyHz,
			FrequencyBands:      result.FrequencyBands,
			ProcessedAt:         timestamppb.Now(),
		}

		// Send response immediately
		if err := stream.Send(resp); err != nil {
			return status.Errorf(codes.Internal, "failed to send: %v", err)
		}

		// Save asynchronously
		go s.saveAnalysisResult(req, result)

		// Increment counter
		s.readingsProcessed.Add(ctx, 1)
	}
}

// AnalyzeBatch handles batch analysis
func (s *AnalyzerService) AnalyzeBatch(ctx context.Context, req *analyzerv1.AnalyzeBatchRequest) (*analyzerv1.AnalyzeBatchResponse, error) {
	if len(req.Readings) == 0 {
		return nil, status.Error(codes.InvalidArgument, "readings list cannot be empty")
	}

	var results []*analyzerv1.AnalyzeResponse
	var totalRiskScore float64

	// Analyze each reading
	for _, readingReq := range req.Readings {
		result := s.analyzeRequest(readingReq)

		resp := &analyzerv1.AnalyzeResponse{
			ReadingId:           readingReq.ReadingId,
			NoiseCategory:       result.NoiseCategory,
			HealthRiskLevel:     result.HealthRiskLevel,
			RiskScore:           result.RiskScore,
			DominantFrequencyHz: result.DominantFrequencyHz,
			FrequencyBands:      result.FrequencyBands,
			ProcessedAt:         timestamppb.Now(),
		}

		results = append(results, resp)
		totalRiskScore += result.RiskScore

		s.readingsProcessed.Add(ctx, 1)
	}

	// Save all results
	go s.saveBatchResults(req.Readings, results)

	// Calculate average risk score
	avgRiskScore := totalRiskScore / float64(len(results))

	return &analyzerv1.AnalyzeBatchResponse{
		Results:         results,
		AverageRiskScore: avgRiskScore,
		ZoneId:          req.ZoneId,
	}, nil
}

// GetZoneAnalysis handles server streaming for zone analysis
func (s *AnalyzerService) GetZoneAnalysis(req *analyzerv1.GetZoneAnalysisRequest, stream analyzerv1.AnalyzerService_GetZoneAnalysisServer) error {
	ctx := stream.Context()

	// Validate
	if req.H3Index == "" {
		return status.Error(codes.InvalidArgument, "h3_index is required")
	}
	if req.UpdateIntervalSeconds < 1 || req.UpdateIntervalSeconds > 60 {
		return status.Error(codes.InvalidArgument, "update_interval_seconds must be between 1 and 60")
	}

	interval := time.Duration(req.UpdateIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("zone analysis stream cancelled")
			return nil
		case <-ticker.C:
			// Query latest analysis for this H3 cell
			records, err := s.repo.GetLatestByH3Index(ctx, req.H3Index, 100)
			if err != nil {
				s.logger.Error("failed to get latest analysis", slog.String("error", err.Error()))
				continue
			}

			if len(records) == 0 {
				continue
			}

			// Aggregate results
			var totalRiskScore float64
			categoryCounts := make(map[commonv1.NoiseCategory]int)

			for _, rec := range records {
				totalRiskScore += rec.RiskScore
				category := parseNoiseCategory(rec.NoiseCategory)
				categoryCounts[category]++
			}

			// Find dominant category
			var dominantCategory commonv1.NoiseCategory
			maxCount := 0
			for cat, count := range categoryCounts {
				if count > maxCount {
					maxCount = count
					dominantCategory = cat
				}
			}

			avgRiskScore := totalRiskScore / float64(len(records))
			riskLevel := calculateRiskLevel(avgRiskScore)

			// Get latest record for the response
			latest := records[0]

			resp := &analyzerv1.GetZoneAnalysisResponse{
				H3Index: req.H3Index,
				LatestAnalysis: &analyzerv1.AnalyzeResponse{
					ReadingId:           latest.ReadingID,
					NoiseCategory:         dominantCategory,
					HealthRiskLevel:       riskLevel,
					RiskScore:             avgRiskScore,
					DominantFrequencyHz: latest.DominantFrequencyHz,
					FrequencyBands:        latest.FrequencyBands,
					ProcessedAt:           timestamppb.New(latest.AnalyzedAt),
				},
				SensorCount: int32(len(records)),
				StreamedAt:  timestamppb.Now(),
			}

			if err := stream.Send(resp); err != nil {
				return status.Errorf(codes.Internal, "failed to send: %v", err)
			}
		}
	}
}

// analyzeRequest runs DSP analysis on a request
func (s *AnalyzerService) analyzeRequest(req *analyzerv1.AnalyzeRequest) *AnalysisResult {
	// Compute FFT on raw samples
	fft := s.dsp.ComputeFFT(req.RawSamples)

	// Get frequency bands
	bands := s.dsp.FrequencyBands(fft, req.SampleRateHz)

	// Get dominant frequency
	dominantFreq := s.dsp.DominantFrequency(fft, req.SampleRateHz)

	// Run full analysis
	return s.dsp.Analyze(req.SensorReading.DecibelLevel, dominantFreq, bands)
}

// saveAnalysisResult saves a single analysis result
func (s *AnalyzerService) saveAnalysisResult(req *analyzerv1.AnalyzeRequest, result *AnalysisResult) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get H3 index
	h3Index := s.h3Util.LatLngToH3Index(req.SensorReading.Latitude, req.SensorReading.Longitude, 9)

	record := &repository.AnalysisRecord{
		ReadingID:           req.ReadingId,
		SensorID:            req.SensorReading.SensorId,
		NoiseCategory:       result.NoiseCategory.String(),
		HealthRiskLevel:     result.HealthRiskLevel.String(),
		RiskScore:           result.RiskScore,
		DominantFrequencyHz: result.DominantFrequencyHz,
		AnalyzedAt:          time.Now(),
		H3Index:             h3Index,
		H3Resolution:        9,
	}

	if err := s.repo.SaveAnalysis(ctx, record); err != nil {
		s.logger.Error("failed to save analysis", slog.String("error", err.Error()))
	}
}

// saveBatchResults saves multiple analysis results
func (s *AnalyzerService) saveBatchResults(reqs []*analyzerv1.AnalyzeRequest, results []*analyzerv1.AnalyzeResponse) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var records []*repository.AnalysisRecord
	for i, req := range reqs {
		result := results[i]
		h3Index := s.h3Util.LatLngToH3Index(req.SensorReading.Latitude, req.SensorReading.Longitude, 9)

		record := &repository.AnalysisRecord{
			ReadingID:           req.ReadingId,
			SensorID:            req.SensorReading.SensorId,
			NoiseCategory:       result.NoiseCategory.String(),
			HealthRiskLevel:     result.HealthRiskLevel.String(),
			RiskScore:           result.RiskScore,
			DominantFrequencyHz: result.DominantFrequencyHz,
			AnalyzedAt:          time.Now(),
			H3Index:             h3Index,
			H3Resolution:        9,
		}
		records = append(records, record)
	}

	if err := s.repo.SaveAnalysisBatch(ctx, records); err != nil {
		s.logger.Error("failed to save batch analysis", slog.String("error", err.Error()))
	}
}

// parseNoiseCategory converts string to enum
func parseNoiseCategory(s string) commonv1.NoiseCategory {
	switch s {
	case "NOISE_CATEGORY_NATURE":
		return commonv1.NoiseCategory_NOISE_CATEGORY_NATURE
	case "NOISE_CATEGORY_TRAFFIC":
		return commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC
	case "NOISE_CATEGORY_CONSTRUCTION":
		return commonv1.NoiseCategory_NOISE_CATEGORY_CONSTRUCTION
	case "NOISE_CATEGORY_INDUSTRIAL":
		return commonv1.NoiseCategory_NOISE_CATEGORY_INDUSTRIAL
	case "NOISE_CATEGORY_CROWD":
		return commonv1.NoiseCategory_NOISE_CATEGORY_CROWD
	default:
		return commonv1.NoiseCategory_NOISE_CATEGORY_UNSPECIFIED
	}
}

// calculateRiskLevel determines risk level from score
func calculateRiskLevel(score float64) commonv1.HealthRiskLevel {
	switch {
	case score < 25:
		return commonv1.HealthRiskLevel_HEALTH_RISK_SAFE
	case score < 50:
		return commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE
	case score < 75:
		return commonv1.HealthRiskLevel_HEALTH_RISK_HIGH
	default:
		return commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL
	}
}

// computeAverage computes the average of a slice
func computeAverage(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// computeStdDev computes the standard deviation of a slice
func computeStdDev(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sumSquaredDiff float64
	for _, v := range values {
		diff := v - mean
		sumSquaredDiff += diff * diff
	}
	return math.Sqrt(sumSquaredDiff / float64(len(values)))
}
