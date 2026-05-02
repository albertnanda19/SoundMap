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
	ingestorv1 "github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/services/ingestor/internal/publisher"
	"github.com/soundmap/soundmap/services/ingestor/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/stretchr/testify/assert"
)

// MockReadingPublisher for testing
type MockReadingPublisher struct {
	PublishedReadings []*commonv1.SensorReading
}

func (m *MockReadingPublisher) PublishReading(ctx context.Context, reading *commonv1.SensorReading) error {
	m.PublishedReadings = append(m.PublishedReadings, reading)
	return nil
}

func (m *MockReadingPublisher) PublishReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) error {
	m.PublishedReadings = append(m.PublishedReadings, readings...)
	return nil
}

func (m *MockReadingPublisher) Close() error {
	return nil
}

// MockReadingRepository for testing
type MockReadingRepository struct {
	SaveReadingFunc          func(ctx context.Context, reading *commonv1.SensorReading) (string, error)
	SaveReadingsBatchFunc    func(ctx context.Context, readings []*commonv1.SensorReading) (int64, error)
	GetSensorLastReadingFunc func(ctx context.Context, sensorID string) (*repository.SensorStatus, error)
	GetReadingCountTodayFunc func(ctx context.Context, sensorID string) (int64, error)
}

func (m *MockReadingRepository) SaveReading(ctx context.Context, reading *commonv1.SensorReading) (string, error) {
	if m.SaveReadingFunc != nil {
		return m.SaveReadingFunc(ctx, reading)
	}
	return uuid.New().String(), nil
}

func (m *MockReadingRepository) SaveReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) (int64, error) {
	if m.SaveReadingsBatchFunc != nil {
		return m.SaveReadingsBatchFunc(ctx, readings)
	}
	return int64(len(readings)), nil
}

func (m *MockReadingRepository) GetSensorLastReading(ctx context.Context, sensorID string) (*repository.SensorStatus, error) {
	if m.GetSensorLastReadingFunc != nil {
		return m.GetSensorLastReadingFunc(ctx, sensorID)
	}
	return &repository.SensorStatus{
		SensorID:           sensorID,
		LastDecibelLevel:   50.0,
		LastSeenAt:         time.Now(),
		TotalReadingsToday: 100,
	}, nil
}

func (m *MockReadingRepository) GetReadingCountToday(ctx context.Context, sensorID string) (int64, error) {
	if m.GetReadingCountTodayFunc != nil {
		return m.GetReadingCountTodayFunc(ctx, sensorID)
	}
	return 100, nil
}

func createService(repo repository.ReadingRepository, pub publisher.ReadingPublisher) (*IngestorService, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")
	return NewIngestorService(repo, pub, logger, meter)
}

func TestIngestSingleReading_ValidReading(t *testing.T) {
	repo := &MockReadingRepository{
		SaveReadingFunc: func(ctx context.Context, reading *commonv1.SensorReading) (string, error) {
			return "test-reading-id", nil
		},
	}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "sensor-001",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 65.5,
			FrequencyHz:  1000.0,
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	resp, err := svc.IngestSingleReading(context.Background(), req)
	assert.NoError(t, err)
	assert.True(t, resp.Accepted)
	assert.Equal(t, "test-reading-id", resp.ReadingId)
	assert.Empty(t, resp.RejectionReason)
}

func TestIngestSingleReading_EmptySensorID(t *testing.T) {
	repo := &MockReadingRepository{}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 65.5,
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	resp, err := svc.IngestSingleReading(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, resp.Accepted)
	assert.Contains(t, resp.RejectionReason, "sensor_id is required")
}

func TestIngestSingleReading_DecibelOutOfRange(t *testing.T) {
	repo := &MockReadingRepository{}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "sensor-001",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 250.0, // Out of range
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	resp, err := svc.IngestSingleReading(context.Background(), req)
	assert.NoError(t, err)
	assert.False(t, resp.Accepted)
	assert.Contains(t, resp.RejectionReason, "decibel_level must be between 0 and 200")
}

func TestIngestSingleReading_RepositoryError(t *testing.T) {
	repo := &MockReadingRepository{
		SaveReadingFunc: func(ctx context.Context, reading *commonv1.SensorReading) (string, error) {
			return "", errors.New("database error")
		},
	}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.IngestSingleReadingRequest{
		SensorReading: &commonv1.SensorReading{
			SensorId:     "sensor-001",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 65.5,
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
	}

	_, err = svc.IngestSingleReading(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestGetSensorStatus_SensorFound(t *testing.T) {
	repo := &MockReadingRepository{
		GetSensorLastReadingFunc: func(ctx context.Context, sensorID string) (*repository.SensorStatus, error) {
			return &repository.SensorStatus{
				SensorID:           "sensor-001",
				LastDecibelLevel:   75.5,
				LastSeenAt:         time.Now(),
				TotalReadingsToday: 42,
			}, nil
		},
	}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.GetSensorStatusRequest{
		SensorId: "sensor-001",
	}

	resp, err := svc.GetSensorStatus(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "sensor-001", resp.SensorId)
	assert.Equal(t, 75.5, resp.LastDecibelLevel)
	assert.Equal(t, int64(42), resp.TotalReadingsToday)
	assert.True(t, resp.IsActive)
}

func TestGetSensorStatus_SensorNotFound(t *testing.T) {
	repo := &MockReadingRepository{
		GetSensorLastReadingFunc: func(ctx context.Context, sensorID string) (*repository.SensorStatus, error) {
			return nil, errors.New("sensor not found")
		},
	}
	pub := &MockReadingPublisher{}
	svc, err := createService(repo, pub)
	assert.NoError(t, err)

	req := &ingestorv1.GetSensorStatusRequest{
		SensorId: "non-existent-sensor",
	}

	_, err = svc.GetSensorStatus(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "non-existent-sensor not found")
}
