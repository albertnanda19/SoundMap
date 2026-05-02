package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MockReadingRepository is a mock implementation for testing
type MockReadingRepository struct {
	SaveReadingFunc          func(ctx context.Context, reading *commonv1.SensorReading) (string, error)
	SaveReadingsBatchFunc    func(ctx context.Context, readings []*commonv1.SensorReading) (int64, error)
	GetSensorLastReadingFunc func(ctx context.Context, sensorID string) (*SensorStatus, error)
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

func (m *MockReadingRepository) GetSensorLastReading(ctx context.Context, sensorID string) (*SensorStatus, error) {
	if m.GetSensorLastReadingFunc != nil {
		return m.GetSensorLastReadingFunc(ctx, sensorID)
	}
	return &SensorStatus{
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

func TestMockReadingRepository_SaveReading(t *testing.T) {
	mock := &MockReadingRepository{
		SaveReadingFunc: func(ctx context.Context, reading *commonv1.SensorReading) (string, error) {
			return "test-uuid-123", nil
		},
	}
	
	reading := &commonv1.SensorReading{
		SensorId:     "sensor-001",
		Latitude:     40.7128,
		Longitude:    -74.0060,
		DecibelLevel: 65.5,
		FrequencyHz:  1000.0,
		Timestamp:    timestamppb.Now(),
		SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
	}
	
	id, err := mock.SaveReading(context.Background(), reading)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "test-uuid-123" {
		t.Errorf("expected id 'test-uuid-123', got '%s'", id)
	}
}

func TestMockReadingRepository_SaveReadingsBatch(t *testing.T) {
	mock := &MockReadingRepository{
		SaveReadingsBatchFunc: func(ctx context.Context, readings []*commonv1.SensorReading) (int64, error) {
			return int64(len(readings)), nil
		},
	}
	
	readings := []*commonv1.SensorReading{
		{
			SensorId:     "sensor-001",
			Latitude:     40.7128,
			Longitude:    -74.0060,
			DecibelLevel: 65.5,
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		},
		{
			SensorId:     "sensor-002",
			Latitude:     40.7129,
			Longitude:    -74.0061,
			DecibelLevel: 70.2,
			Timestamp:    timestamppb.Now(),
			SensorType:   commonv1.SensorType_SENSOR_TYPE_MOBILE,
		},
	}
	
	count, err := mock.SaveReadingsBatch(context.Background(), readings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}
