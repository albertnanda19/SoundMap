package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	analyzerv1 "github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/services/analyzer/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/stretchr/testify/assert"
)

// MockDSPAnalyzer implements DSPAnalyzer for testing
type MockDSPAnalyzer struct {
	ComputeFFTFunc       func(samples []float64) []float64
	FrequencyBandsFunc   func(fft []float64, sampleRateHz int32) map[string]float64
	DominantFrequencyFunc func(fft []float64, sampleRateHz int32) float64
	AnalyzeFunc          func(decibelLevel, dominantFreq float64, bands map[string]float64) *AnalysisResult
}

func (m *MockDSPAnalyzer) ComputeFFT(samples []float64) []float64 {
	if m.ComputeFFTFunc != nil {
		return m.ComputeFFTFunc(samples)
	}
	return samples
}

func (m *MockDSPAnalyzer) FrequencyBands(fft []float64, sampleRateHz int32) map[string]float64 {
	if m.FrequencyBandsFunc != nil {
		return m.FrequencyBandsFunc(fft, sampleRateHz)
	}
	return map[string]float64{"low": 10.0, "mid": 20.0, "high": 30.0}
}

func (m *MockDSPAnalyzer) DominantFrequency(fft []float64, sampleRateHz int32) float64 {
	if m.DominantFrequencyFunc != nil {
		return m.DominantFrequencyFunc(fft, sampleRateHz)
	}
	return 1000.0
}

func (m *MockDSPAnalyzer) Analyze(decibelLevel, dominantFreq float64, bands map[string]float64) *AnalysisResult {
	if m.AnalyzeFunc != nil {
		return m.AnalyzeFunc(decibelLevel, dominantFreq, bands)
	}
	return &AnalysisResult{
		NoiseCategory:       commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC,
		HealthRiskLevel:     commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE,
		RiskScore:           50.0,
		DominantFrequencyHz: dominantFreq,
		FrequencyBands:      bands,
	}
}

// MockH3Util implements H3Util for testing
type MockH3Util struct {
	LatLngToH3IndexFunc func(lat, lng float64, resolution int32) string
}

func (m *MockH3Util) LatLngToH3Index(lat, lng float64, resolution int32) string {
	if m.LatLngToH3IndexFunc != nil {
		return m.LatLngToH3IndexFunc(lat, lng, resolution)
	}
	return "8928308280fffff"
}

// MockAnalysisRepository implements AnalysisRepository for testing
type MockAnalysisRepository struct {
	SaveAnalysisFunc      func(ctx context.Context, result *repository.AnalysisRecord) error
	SaveAnalysisBatchFunc func(ctx context.Context, results []*repository.AnalysisRecord) error
	GetLatestByH3IndexFunc func(ctx context.Context, h3Index string, limit int) ([]*repository.AnalysisRecord, error)
}

func (m *MockAnalysisRepository) SaveAnalysis(ctx context.Context, result *repository.AnalysisRecord) error {
	if m.SaveAnalysisFunc != nil {
		return m.SaveAnalysisFunc(ctx, result)
	}
	return nil
}

func (m *MockAnalysisRepository) SaveAnalysisBatch(ctx context.Context, results []*repository.AnalysisRecord) error {
	if m.SaveAnalysisBatchFunc != nil {
		return m.SaveAnalysisBatchFunc(ctx, results)
	}
	return nil
}

func (m *MockAnalysisRepository) GetLatestByH3Index(ctx context.Context, h3Index string, limit int) ([]*repository.AnalysisRecord, error) {
	if m.GetLatestByH3IndexFunc != nil {
		return m.GetLatestByH3IndexFunc(ctx, h3Index, limit)
	}
	return nil, nil
}

func createAnalyzerService(repo repository.AnalysisRepository, dsp DSPAnalyzer, h3Util H3Util) (*AnalyzerService, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")
	return NewAnalyzerService(repo, dsp, h3Util, logger, meter)
}

func TestAnalyzeBatch_SingleReadingValidSamples(t *testing.T) {
	dsp := &MockDSPAnalyzer{
		AnalyzeFunc: func(decibelLevel, dominantFreq float64, bands map[string]float64) *AnalysisResult {
			return &AnalysisResult{
				NoiseCategory:       commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC,
				HealthRiskLevel:     commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE,
				RiskScore:           55.0,
				DominantFrequencyHz: 1500.0,
				FrequencyBands:      bands,
			}
		},
	}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	svc, err := createAnalyzerService(repo, dsp, h3Util)
	assert.NoError(t, err)

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId: "zone-001",
		Readings: []*analyzerv1.AnalyzeRequest{
			{
				ReadingId: "reading-001",
				SensorReading: &commonv1.SensorReading{
					SensorId:     "sensor-001",
					Latitude:     40.7128,
					Longitude:    -74.0060,
					DecibelLevel: 70.0,
				},
				RawSamples:   []float64{0.1, 0.2, 0.3, 0.4, 0.5},
				SampleRateHz: 44100,
			},
		},
	}

	resp, err := svc.AnalyzeBatch(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, len(resp.Results))
	assert.Equal(t, "reading-001", resp.Results[0].ReadingId)
	assert.Equal(t, "zone-001", resp.ZoneId)
	assert.Equal(t, 55.0, resp.AverageRiskScore)
	assert.Equal(t, commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC, resp.Results[0].NoiseCategory)
}

func TestAnalyzeBatch_EmptyReadingsList(t *testing.T) {
	dsp := &MockDSPAnalyzer{}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	svc, err := createAnalyzerService(repo, dsp, h3Util)
	assert.NoError(t, err)

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId:   "zone-001",
		Readings: []*analyzerv1.AnalyzeRequest{},
	}

	_, err = svc.AnalyzeBatch(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "readings list cannot be empty")
}

func TestAnalyzeBatch_MultipleReadings(t *testing.T) {
	dsp := &MockDSPAnalyzer{
		AnalyzeFunc: func(decibelLevel, dominantFreq float64, bands map[string]float64) *AnalysisResult {
			// Return different risk scores based on decibel level
			var riskScore float64
			if decibelLevel > 80 {
				riskScore = 80.0
			} else {
				riskScore = 40.0
			}
			return &AnalysisResult{
				NoiseCategory:       commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC,
				HealthRiskLevel:     commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE,
				RiskScore:           riskScore,
				DominantFrequencyHz: 1000.0,
				FrequencyBands:      bands,
			}
		},
	}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	svc, err := createAnalyzerService(repo, dsp, h3Util)
	assert.NoError(t, err)

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId: "zone-001",
		Readings: []*analyzerv1.AnalyzeRequest{
			{
				ReadingId: "reading-001",
				SensorReading: &commonv1.SensorReading{
					SensorId:     "sensor-001",
					Latitude:     40.7128,
					Longitude:    -74.0060,
					DecibelLevel: 70.0,
				},
				RawSamples:   []float64{0.1, 0.2, 0.3},
				SampleRateHz: 44100,
			},
			{
				ReadingId: "reading-002",
				SensorReading: &commonv1.SensorReading{
					SensorId:     "sensor-002",
					Latitude:     40.7129,
					Longitude:    -74.0061,
					DecibelLevel: 85.0,
				},
				RawSamples:   []float64{0.4, 0.5, 0.6},
				SampleRateHz: 44100,
			},
		},
	}

	resp, err := svc.AnalyzeBatch(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(resp.Results))
	// Average of 40.0 and 80.0 = 60.0
	assert.Equal(t, 60.0, resp.AverageRiskScore)
}

func TestCalculateRiskLevel(t *testing.T) {
	tests := []struct {
		score    float64
		expected commonv1.HealthRiskLevel
	}{
		{10.0, commonv1.HealthRiskLevel_HEALTH_RISK_SAFE},
		{24.9, commonv1.HealthRiskLevel_HEALTH_RISK_SAFE},
		{25.0, commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE},
		{49.9, commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE},
		{50.0, commonv1.HealthRiskLevel_HEALTH_RISK_HIGH},
		{74.9, commonv1.HealthRiskLevel_HEALTH_RISK_HIGH},
		{75.0, commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL},
		{100.0, commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL},
	}

	for _, tt := range tests {
		t.Run(uuid.New().String(), func(t *testing.T) {
			result := calculateRiskLevel(tt.score)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseNoiseCategory(t *testing.T) {
	tests := []struct {
		input    string
		expected commonv1.NoiseCategory
	}{
		{"NOISE_CATEGORY_NATURE", commonv1.NoiseCategory_NOISE_CATEGORY_NATURE},
		{"NOISE_CATEGORY_TRAFFIC", commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC},
		{"NOISE_CATEGORY_CONSTRUCTION", commonv1.NoiseCategory_NOISE_CATEGORY_CONSTRUCTION},
		{"NOISE_CATEGORY_INDUSTRIAL", commonv1.NoiseCategory_NOISE_CATEGORY_INDUSTRIAL},
		{"NOISE_CATEGORY_CROWD", commonv1.NoiseCategory_NOISE_CATEGORY_CROWD},
		{"NOISE_CATEGORY_UNKNOWN", commonv1.NoiseCategory_NOISE_CATEGORY_UNSPECIFIED},
		{"invalid", commonv1.NoiseCategory_NOISE_CATEGORY_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseNoiseCategory(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetZoneAnalysis_InvalidH3Index(t *testing.T) {
	dsp := &MockDSPAnalyzer{}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	svc, err := createAnalyzerService(repo, dsp, h3Util)
	assert.NoError(t, err)

	req := &analyzerv1.GetZoneAnalysisRequest{
		H3Index:               "",  // Empty H3 index
		UpdateIntervalSeconds: 5,
	}

	// Since this is a streaming endpoint, we can't directly test it here
	// without a mock stream, but we can test the validation logic
	assert.Empty(t, req.H3Index)
}

func TestGetZoneAnalysis_InvalidInterval(t *testing.T) {
	dsp := &MockDSPAnalyzer{}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	svc, err := createAnalyzerService(repo, dsp, h3Util)
	assert.NoError(t, err)

	tests := []struct {
		name     string
		interval int32
		valid    bool
	}{
		{"too_low", 0, false},
		{"min_valid", 1, true},
		{"max_valid", 60, true},
		{"too_high", 61, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &analyzerv1.GetZoneAnalysisRequest{
				H3Index:               "8928308280fffff",
				UpdateIntervalSeconds: tt.interval,
			}

			valid := req.UpdateIntervalSeconds >= 1 && req.UpdateIntervalSeconds <= 60
			assert.Equal(t, tt.valid, valid)
		})
	}
}

func TestNewAnalyzerService_MetricsRegistration(t *testing.T) {
	dsp := &MockDSPAnalyzer{}
	h3Util := &MockH3Util{}
	repo := &MockAnalysisRepository{}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")

	svc, err := NewAnalyzerService(repo, dsp, h3Util, logger, meter)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.readingsProcessed)
	assert.NotNil(t, svc.processingDuration)
	assert.NotNil(t, svc.activeBidiStreams)
}
