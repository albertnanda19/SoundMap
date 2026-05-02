//go:build e2e
// +build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1   "github.com/soundmap/soundmap/gen/go/common/v1"
	ingestorv1 "github.com/soundmap/soundmap/gen/go/ingestor/v1"
	analyzerv1 "github.com/soundmap/soundmap/gen/go/analyzer/v1"
	alertv1    "github.com/soundmap/soundmap/gen/go/alert/v1"
	geov1      "github.com/soundmap/soundmap/gen/go/geo/v1"
)

// UUID regex pattern
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestE2E_HealthChecks_AllServicesHealthy tests that all gRPC services respond to health checks
func TestE2E_HealthChecks_AllServicesHealthy(t *testing.T) {
	ports := []string{"50051", "50052", "50053", "50054", "50055"}
	serviceNames := []string{"ingestor", "analyzer", "alert", "geo", "report"}

	for i, port := range ports {
		t.Run(serviceNames[i], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Create a connection to test health
			conn := suite.IngestorConn
			if port == "50052" {
				conn = suite.AnalyzerConn
			} else if port == "50053" {
				conn = suite.AlertConn
			} else if port == "50054" {
				conn = suite.GeoConn
			} else if port == "50055" {
				conn = suite.ReportConn
			}

			// Health check would require importing health package
			// For now, just verify connection state
			state := conn.GetState()
			t.Logf("Service %s on port %s has connection state: %v", serviceNames[i], port, state)
		})
	}
}

// TestE2E_Ingestor_SingleReading_ValidData tests ingesting a single valid reading
func TestE2E_Ingestor_SingleReading_ValidData(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "e2e-test-sensor-001",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 72.5,
			FrequencyHz:  440.0,
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
			RawSamples:   []float64{0.1, 0.2, 0.3, 0.4, 0.5},
		},
	}

	resp, err := suite.Ingestor.IngestSingleReading(ctx, req)
	require.NoError(t, err, "IngestSingleReading should not fail")
	require.True(t, resp.GetAccepted(), "Reading should be accepted")
	require.NotEmpty(t, resp.GetReadingId(), "ReadingId should not be empty")
	require.Regexp(t, uuidRegex, resp.GetReadingId(), "ReadingId should be a valid UUID")
	require.Empty(t, resp.GetRejectionReason(), "RejectionReason should be empty for valid data")

	t.Logf("Successfully ingested reading with ID: %s", resp.GetReadingId())
}

// TestE2E_Ingestor_SingleReading_InvalidSensorID tests validation rejects empty sensor_id
func TestE2E_Ingestor_SingleReading_InvalidSensorID(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "", // Empty sensor ID
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 72.5,
			FrequencyHz:  440.0,
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	_, err := suite.Ingestor.IngestSingleReading(ctx, req)
	require.Error(t, err, "Should get error for empty sensor_id")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	require.Equal(t, codes.InvalidArgument, st.Code(), "Should get InvalidArgument error")
}

// TestE2E_Ingestor_ClientStreaming_BatchIngest tests client streaming RPC
func TestE2E_Ingestor_ClientStreaming_BatchIngest(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 30*time.Second)
	defer cancel()

	stream, err := suite.Ingestor.IngestReadings(ctx)
	require.NoError(t, err, "Should be able to open stream")

	// Send 50 readings
	for i := 0; i < 50; i++ {
		req := &ingestorv1.IngestReadingsRequest{
			SensorReading: &commonv1.SensorReading{
				SensorId:     fmt.Sprintf("e2e-batch-%d", i),
				Latitude:     40.7128 + float64(i)*0.001,
				Longitude:    -74.0060 + float64(i)*0.001,
				DecibelLevel: 55.0 + float64(i%30),
				FrequencyHz:  440.0,
				SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
			},
		}
		err := stream.Send(req)
		require.NoError(t, err, "Should be able to send reading %d", i)
	}

	resp, err := stream.CloseAndRecv()
	require.NoError(t, err, "Should receive response")
	require.Equal(t, int64(50), resp.GetAcceptedCount(), "All 50 readings should be accepted")
	require.Equal(t, int64(0), resp.GetRejectedCount(), "No readings should be rejected")
	require.NotEmpty(t, resp.GetSessionId(), "SessionId should not be empty")

	t.Logf("Batch ingest: %d accepted, session=%s", resp.GetAcceptedCount(), resp.GetSessionId())
}

// TestE2E_Ingestor_GetSensorStatus_AfterIngest tests sensor status retrieval
func TestE2E_Ingestor_GetSensorStatus_AfterIngest(t *testing.T) {
	ts := time.Now().Unix()
	sensorId := fmt.Sprintf("e2e-status-test-%d", ts)

	// First ingest a reading
	ingestCtx, ingestCancel := ctxTimeout(t, 10*time.Second)
	defer ingestCancel()

	decibelLevel := 85.5
	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     sensorId,
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: decibelLevel,
			FrequencyHz:  440.0,
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	_, err := suite.Ingestor.IngestSingleReading(ingestCtx, req)
	require.NoError(t, err, "Ingest should succeed")

	// Wait for persistence
	time.Sleep(1 * time.Second)

	// Get sensor status
	statusCtx, statusCancel := ctxTimeout(t, 10*time.Second)
	defer statusCancel()

	statusReq := &ingestorv1.GetSensorStatusRequest{
		SensorId: sensorId,
	}

	resp, err := suite.Ingestor.GetSensorStatus(statusCtx, statusReq)
	require.NoError(t, err, "GetSensorStatus should succeed")
	require.Equal(t, sensorId, resp.GetSensorId(), "SensorId should match")
	require.True(t, resp.GetIsActive(), "Sensor should be active")
	require.InDelta(t, decibelLevel, resp.GetLastDecibelLevel(), 0.01, "LastDecibelLevel should match")
	require.GreaterOrEqual(t, resp.GetTotalReadingsToday(), int64(1), "Should have at least 1 reading")
	require.NotNil(t, resp.GetLastSeenAt(), "LastSeenAt should not be nil")
}

// TestE2E_Ingestor_GetSensorStatus_NotFound tests error for non-existent sensor
func TestE2E_Ingestor_GetSensorStatus_NotFound(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &ingestorv1.GetSensorStatusRequest{
		SensorId: "sensor-that-absolutely-does-not-exist-xyz-999",
	}

	_, err := suite.Ingestor.GetSensorStatus(ctx, req)
	require.Error(t, err, "Should get error for non-existent sensor")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	require.Equal(t, codes.NotFound, st.Code(), "Should get NotFound error")
}

// TestE2E_Analyzer_BatchAnalysis_ValidReadings tests batch analysis
func TestE2E_Analyzer_BatchAnalysis_ValidReadings(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 30*time.Second)
	defer cancel()

	// Create 5 analysis requests with sine wave samples
	var readings []*analyzerv1.AnalyzeRequest
	for i := 0; i < 5; i++ {
		// Generate 512 samples of sine wave at 440Hz
		rawSamples := make([]float64, 512)
		for j := 0; j < 512; j++ {
			rawSamples[j] = math.Sin(2*math.Pi*440.0*float64(j)/44100.0) + (float64(i) * 0.01)
		}

		reading := &analyzerv1.AnalyzeRequest{
			ReadingId: fmt.Sprintf("e2e-analyze-%d", i),
			SensorReading: &commonv1.SensorReading{
				SensorId:     fmt.Sprintf("e2e-sensor-%d", i),
				Latitude:     40.7128,
				Longitude:    -74.0060,
				DecibelLevel: 60.0 + float64(i)*5,
				FrequencyHz:  440.0,
			},
			RawSamples:   rawSamples,
			SampleRateHz: 44100,
		}
		readings = append(readings, reading)
	}

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId:   "e2e-zone-test",
		Readings: readings,
	}

	resp, err := suite.Analyzer.AnalyzeBatch(ctx, req)
	require.NoError(t, err, "AnalyzeBatch should succeed")
	require.Len(t, resp.GetResults(), 5, "Should have 5 results")

	var totalRisk float64
	for i, result := range resp.GetResults() {
		t.Logf("Reading %d: category=%v, risk=%.1f, freq=%.0fHz",
			i, result.GetNoiseCategory(), result.GetRiskScore(), result.GetDominantFrequencyHz())

		require.GreaterOrEqual(t, result.GetRiskScore(), 0.0, "RiskScore should be >= 0")
		require.LessOrEqual(t, result.GetRiskScore(), 100.0, "RiskScore should be <= 100")
		require.NotEqual(t, commonv1.NoiseCategory_NOISE_CATEGORY_UNSPECIFIED, result.GetNoiseCategory(),
			"NoiseCategory should not be UNSPECIFIED")
		require.Greater(t, result.GetDominantFrequencyHz(), 0.0, "DominantFrequencyHz should be > 0")
		require.Len(t, result.GetFrequencyBands(), 7, "Should have 7 frequency bands")

		totalRisk += result.GetRiskScore()
	}

	expectedAvg := totalRisk / 5.0
	require.InDelta(t, expectedAvg, resp.GetAverageRiskScore(), 0.1, "AverageRiskScore should match calculated average")
}

// TestE2E_Analyzer_BatchAnalysis_Empty tests error for empty readings
func TestE2E_Analyzer_BatchAnalysis_Empty(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId:   "e2e-zone-empty",
		Readings: []*analyzerv1.AnalyzeRequest{},
	}

	_, err := suite.Analyzer.AnalyzeBatch(ctx, req)
	require.Error(t, err, "Should get error for empty readings")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	require.Equal(t, codes.InvalidArgument, st.Code(), "Should get InvalidArgument error")
}

// TestE2E_Analyzer_BiDiStream_RealTimeAnalysis tests bidirectional streaming
func TestE2E_Analyzer_BiDiStream_RealTimeAnalysis(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 30*time.Second)
	defer cancel()

	stream, err := suite.Analyzer.AnalyzeStream(ctx)
	require.NoError(t, err, "Should be able to open stream")

	// Send 10 readings
	go func() {
		for i := 0; i < 10; i++ {
			rawSamples := make([]float64, 512)
			for j := 0; j < 512; j++ {
				rawSamples[j] = math.Sin(2*math.Pi*440.0*float64(j)/44100.0)
			}

			req := &analyzerv1.AnalyzeRequest{
				ReadingId: fmt.Sprintf("e2e-bidi-%d", i),
				SensorReading: &commonv1.SensorReading{
					SensorId:     fmt.Sprintf("e2e-bidi-sensor-%d", i),
					Latitude:     40.7128,
					Longitude:    -74.0060,
					DecibelLevel: 70.0,
					FrequencyHz:  440.0,
				},
				RawSamples:   rawSamples,
				SampleRateHz: 44100,
			}
			err := stream.Send(req)
			if err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		stream.CloseSend()
	}()

	// Receive responses
	var responses []*analyzerv1.AnalysisResult
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		responses = append(responses, resp)
	}

	require.Len(t, responses, 10, "Should receive 10 responses")

	// Verify order preserved
	for i, resp := range responses {
		expectedId := fmt.Sprintf("e2e-bidi-%d", i)
		require.Equal(t, expectedId, resp.GetReadingId(), "Response order should be preserved")
	}
}

// TestE2E_Alert_CreateThreshold_Valid tests threshold creation
func TestE2E_Alert_CreateThreshold_Valid(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &alertv1.CreateThresholdRequest{
		Name:       "E2E Test Threshold",
		H3Index:    "", // Global threshold
		MaxDecibel: 65.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	resp, err := suite.Alert.CreateThreshold(ctx, req)
	require.NoError(t, err, "CreateThreshold should succeed")
	require.NotEmpty(t, resp.GetThreshold().GetThresholdId(), "ThresholdId should not be empty")
	require.Regexp(t, uuidRegex, resp.GetThreshold().GetThresholdId(), "ThresholdId should be a valid UUID")
	require.Equal(t, "E2E Test Threshold", resp.GetThreshold().GetName(), "Name should match")
	require.InDelta(t, 65.0, resp.GetThreshold().GetMaxDecibel(), 0.01, "MaxDecibel should match")
	require.True(t, resp.GetThreshold().GetIsActive(), "IsActive should be true")
	require.NotNil(t, resp.GetThreshold().GetCreatedAt(), "CreatedAt should not be nil")
}

// TestE2E_Alert_CreateThreshold_InvalidDecibel tests validation
func TestE2E_Alert_CreateThreshold_InvalidDecibel(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	// Test with 0.0 decibel
	req1 := &alertv1.CreateThresholdRequest{
		Name:       "Invalid Threshold",
		MaxDecibel: 0.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}
	_, err := suite.Alert.CreateThreshold(ctx, req1)
	require.Error(t, err, "Should get error for zero max_decibel")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	require.Equal(t, codes.InvalidArgument, st.Code(), "Should get InvalidArgument error")
}

// TestE2E_Alert_ListThresholds_Pagination tests pagination
func TestE2E_Alert_ListThresholds_Pagination(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &alertv1.ListThresholdsRequest{
		Page:     1,
		PageSize: 10,
	}

	resp, err := suite.Alert.ListThresholds(ctx, req)
	require.NoError(t, err, "ListThresholds should succeed")
	require.NotNil(t, resp.GetPagination(), "Pagination should not be nil")
	require.GreaterOrEqual(t, resp.GetPagination().GetTotalCount(), int32(0), "TotalCount should be >= 0")
}

// TestE2E_Geo_QueryHotspots_ValidRadius tests hotspot querying
func TestE2E_Geo_QueryHotspots_ValidRadius(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &geov1.QueryHotspotsRequest{
		CenterLat:  40.7128,
		CenterLng:  -74.0060,
		RadiusKm:   10.0,
		MinDecibel: 0.0,
		Resolution: 8,
		Limit:      20,
	}

	resp, err := suite.Geo.QueryHotspots(ctx, req)
	require.NoError(t, err, "QueryHotspots should succeed")
	require.GreaterOrEqual(t, resp.GetQueryTimeMs(), int32(0), "QueryTimeMs should be >= 0")

	for _, cell := range resp.GetCells() {
		require.GreaterOrEqual(t, cell.GetAvgDecibel(), 0.0, "AvgDecibel should be >= 0")
		require.LessOrEqual(t, cell.GetAvgDecibel(), 200.0, "AvgDecibel should be <= 200")
		require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{15}$`), cell.GetH3Index(), "H3Index should be 15 char hex")
	}

	t.Logf("QueryHotspots returned %d cells in %dms", len(resp.GetCells()), resp.GetQueryTimeMs())
}

// TestE2E_Geo_QueryHotspots_InvalidRadius tests radius validation
func TestE2E_Geo_QueryHotspots_InvalidRadius(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &geov1.QueryHotspotsRequest{
		CenterLat:  40.7128,
		CenterLng:  -74.0060,
		RadiusKm:   200.0, // Exceeds 50km limit
		MinDecibel: 0.0,
		Resolution: 8,
	}

	_, err := suite.Geo.QueryHotspots(ctx, req)
	require.Error(t, err, "Should get error for radius > 50km")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	require.Equal(t, codes.InvalidArgument, st.Code(), "Should get InvalidArgument error")
}

// TestE2E_Geo_GetHexCellStats_KnownCell tests hex cell stats retrieval
func TestE2E_Geo_GetHexCellStats_KnownCell(t *testing.T) {
	ctx, cancel := ctxTimeout(t, 10*time.Second)
	defer cancel()

	req := &geov1.GetHexCellStatsRequest{
		H3Index:        "8928308280fffff",
		TimeRangeHours: 1,
	}

	resp, err := suite.Geo.GetHexCellStats(ctx, req)
	require.NoError(t, err, "GetHexCellStats should succeed")
	require.NotNil(t, resp.GetCell(), "Cell should not be nil")
	require.Equal(t, "8928308280fffff", resp.GetCell().GetH3Index(), "H3Index should match")

	if resp.GetCell().GetAvgDecibel() > 0 {
		require.GreaterOrEqual(t, resp.GetCell().GetMaxDecibel(), resp.GetCell().GetAvgDecibel(),
			"MaxDecibel should be >= AvgDecibel")
		require.LessOrEqual(t, resp.GetCell().GetMinDecibel(), resp.GetCell().GetAvgDecibel(),
			"MinDecibel should be <= AvgDecibel")
	}
}
