package integration

import (
	"testing"
	"time"

	"github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIngestSingleReading_Valid(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	reading := makeSensorReading("test-sensor-01", 40.7128, -74.0060, 75.5, 440.0)
	req := &ingestorv1.IngestSingleReadingRequest{Reading: reading}

	resp, err := ingestorClient.IngestSingleReading(ctx, req)
	assert.NoError(t, err)
	assert.True(t, resp.Accepted)
	assert.NotEmpty(t, resp.ReadingId)
	assert.True(t, isValidUUID(resp.ReadingId))
}

func TestIngestSingleReading_InvalidSensorID(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	reading := makeSensorReading("", 40.7128, -74.0060, 75.5, 440.0) // Empty sensor_id
	req := &ingestorv1.IngestSingleReadingRequest{Reading: reading}

	_, err := ingestorClient.IngestSingleReading(ctx, req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestIngestReadings_ClientStream(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	stream, err := ingestorClient.IngestReadings(ctx)
	assert.NoError(t, err)
	defer stream.CloseSend()

	// Send 10 valid readings
	for i := 0; i < 10; i++ {
		reading := makeSensorReading("test-stream-01", 40.7128, -74.0060, 75.5+float64(i), 440.0)
		err := stream.Send(&ingestorv1.IngestSingleReadingRequest{Reading: reading})
		assert.NoError(t, err)
	}

	resp, err := stream.CloseAndRecv()
	assert.NoError(t, err)
	assert.Equal(t, int64(10), resp.ReadingsAccepted)
	assert.Equal(t, int64(0), resp.ReadingsRejected)
	assert.NotEmpty(t, resp.SessionId)
}

func TestIngestReadings_MixedValid(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	stream, err := ingestorClient.IngestReadings(ctx)
	assert.NoError(t, err)
	defer stream.CloseSend()

	// Send 5 valid + 5 invalid
	for i := 0; i < 10; i++ {
		var reading *commonv1.SensorReading
		if i%2 == 0 {
			reading = makeSensorReading("valid-sensor", 40.7128, -74.0060, 75.5, 440.0)
		} else {
			reading = makeSensorReading("", 40.7128, -74.0060, 75.5, 440.0) // Invalid
		}
		err := stream.Send(&ingestorv1.IngestSingleReadingRequest{Reading: reading})
		assert.NoError(t, err)
	}

	resp, err := stream.CloseAndRecv()
	assert.NoError(t, err)
	assert.Equal(t, int64(5), resp.ReadingsAccepted)
	assert.Equal(t, int64(5), resp.ReadingsRejected)
}

func TestGetSensorStatus_AfterIngest(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	sensorID := "test-sensor-integration-01"
	reading := makeSensorReading(sensorID, 40.7128, -74.0060, 82.3, 440.0)
	
	_, err := ingestorClient.IngestSingleReading(ctx, &ingestorv1.IngestSingleReadingRequest{Reading: reading})
	assert.NoError(t, err)

	// Wait for persistence
	time.Sleep(500 * time.Millisecond)

	resp, err := ingestorClient.GetSensorStatus(ctx, &ingestorv1.GetSensorStatusRequest{SensorId: sensorID})
	assert.NoError(t, err)
	assert.True(t, resp.IsActive)
	assert.InDelta(t, 82.3, resp.LastDecibelLevel, 0.1)
}
