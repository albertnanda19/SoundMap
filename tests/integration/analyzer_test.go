package integration

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/stretchr/testify/assert"
)

func TestAnalyzeBatch_Valid(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	// Create 3 readings with 512 raw samples each
	readings := make([]*analyzerv1.AnalyzeRequest, 3)
	for i := 0; i < 3; i++ {
		readings[i] = &analyzerv1.AnalyzeRequest{
			ReadingId:   "batch-test-" + string(rune('0'+i)),
			SensorReading: makeSensorReading("sensor-"+string(rune('0'+i)), 40.7128, -74.0060, 75.5+float64(i)*5, 440.0),
			RawSamples:    generateRawSamples(512),
			SampleRateHz:  44100,
		}
	}

	req := &analyzerv1.AnalyzeBatchRequest{
		ZoneId:   "zone-test-01",
		Readings: readings,
	}

	resp, err := analyzerClient.AnalyzeBatch(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(resp.Results))

	for _, result := range resp.Results {
		assert.True(t, result.RiskScore >= 0 && result.RiskScore <= 100)
		assert.NotEqual(t, commonv1.NoiseCategory_NOISE_CATEGORY_UNSPECIFIED, result.NoiseCategory)
	}
}

func TestAnalyzeStream_BidirectionalFlow(t *testing.T) {
	t.Parallel()

	// Use context with 10s timeout to avoid deadlock
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = newAuthCtxWithContext(ctx, t)

	stream, err := analyzerClient.AnalyzeStream(ctx)
	assert.NoError(t, err)
	defer stream.CloseSend()

	sendCount := 5
	receivedCount := 0
	done := make(chan bool)

	// Send in goroutine
	go func() {
		for i := 0; i < sendCount; i++ {
			req := &analyzerv1.AnalyzeRequest{
				ReadingId:   "stream-test-" + string(rune('0'+i)),
				SensorReading: makeSensorReading("stream-sensor", 40.7128, -74.0060, 80.0, 440.0),
				RawSamples:    generateRawSamples(512),
				SampleRateHz:  44100,
			}
			err := stream.Send(req)
			if err != nil {
				return
			}
		}
		stream.CloseSend()
		done <- true
	}()

	// Receive
	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}
		assert.True(t, resp.RiskScore >= 0 && resp.RiskScore <= 100)
		receivedCount++
	}

	<-done
	assert.Equal(t, sendCount, receivedCount)
}

// generateRawSamples creates a sine wave with harmonics
func generateRawSamples(count int) []float64 {
	samples := make([]float64, count)
	freq := 440.0
	sampleRate := 44100.0
	for i := 0; i < count; i++ {
		t := float64(i) / sampleRate
		samples[i] = 0.5*math.Sin(2*math.Pi*freq*t) +
			0.25*math.Sin(2*math.Pi*freq*2*t) +
			0.125*math.Sin(2*math.Pi*freq*3*t)
	}
	return samples
}

// newAuthCtxWithContext wraps context with auth
func newAuthCtxWithContext(ctx context.Context, t *testing.T) context.Context {
	token := generateValidToken(t)
	return context.WithValue(ctx, "authorization", "Bearer "+token)
}
