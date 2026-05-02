package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	ingestorv1 "github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/services/ingestor/internal/publisher"
	"github.com/soundmap/soundmap/services/ingestor/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// IngestorService implements the ingestor gRPC service
type IngestorService struct {
	repo      repository.ReadingRepository
	publisher publisher.ReadingPublisher
	logger    *slog.Logger
	meter     metric.Meter
	
	// metrics
	readingsAccepted   metric.Int64Counter
	readingsRejected   metric.Int64Counter
	activeStreams      metric.Int64UpDownCounter
	batchSizeHistogram metric.Int64Histogram
}

// NewIngestorService creates a new ingestor service
func NewIngestorService(repo repository.ReadingRepository, pub publisher.ReadingPublisher, logger *slog.Logger, meter metric.Meter) (*IngestorService, error) {
	svc := &IngestorService{
		repo:      repo,
		publisher: pub,
		logger:    logger,
		meter:     meter,
	}
	
	var err error
	
	// Register metrics
	svc.readingsAccepted, err = meter.Int64Counter("ingestor_readings_accepted_total", metric.WithDescription("Total number of accepted readings"))
	if err != nil {
		return nil, err
	}
	
	svc.readingsRejected, err = meter.Int64Counter("ingestor_readings_rejected_total", metric.WithDescription("Total number of rejected readings"))
	if err != nil {
		return nil, err
	}
	
	svc.activeStreams, err = meter.Int64UpDownCounter("ingestor_active_streams", metric.WithDescription("Current number of active streaming sessions"))
	if err != nil {
		return nil, err
	}
	
	svc.batchSizeHistogram, err = meter.Int64Histogram("ingestor_batch_size", 
		metric.WithDescription("Distribution of batch sizes"),
		metric.WithExplicitBucketBoundaries(1, 10, 50, 100, 500, 1000),
	)
	if err != nil {
		return nil, err
	}
	
	return svc, nil
}

// validateReading validates a sensor reading
func (s *IngestorService) validateReading(reading *commonv1.SensorReading) error {
	if reading.SensorId == "" {
		return status.Errorf(codes.InvalidArgument, "sensor_id is required")
	}
	if reading.DecibelLevel < 0 || reading.DecibelLevel > 200 {
		return status.Errorf(codes.InvalidArgument, "decibel_level must be between 0 and 200")
	}
	if reading.Latitude < -90 || reading.Latitude > 90 {
		return status.Errorf(codes.InvalidArgument, "latitude must be between -90 and 90")
	}
	if reading.Longitude < -180 || reading.Longitude > 180 {
		return status.Errorf(codes.InvalidArgument, "longitude must be between -180 and 180")
	}
	return nil
}

// IngestReadings handles client streaming of sensor readings
func (s *IngestorService) IngestReadings(stream ingestorv1.IngestorService_IngestReadingsServer) error {
	ctx := stream.Context()
	
	// Track active stream
	s.activeStreams.Add(ctx, 1)
	defer s.activeStreams.Add(ctx, -1)
	
	sessionID := uuid.New().String()
	var acceptedCount, rejectedCount int64
	var batch []*commonv1.SensorReading
	var batchMutex sync.Mutex
	
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	done := make(chan struct{})
	defer close(done)
	
	// Batch writer goroutine
	go func() {
		for {
			select {
			case <-ticker.C:
				batchMutex.Lock()
				if len(batch) > 0 {
					s.flushBatch(ctx, batch, &acceptedCount, &rejectedCount)
					batch = batch[:0]
				}
				batchMutex.Unlock()
			case <-done:
				return
			}
		}
	}()
	
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			// Flush remaining batch
			batchMutex.Lock()
			if len(batch) > 0 {
				s.flushBatch(ctx, batch, &acceptedCount, &rejectedCount)
			}
			batchMutex.Unlock()
			
			s.batchSizeHistogram.Record(ctx, acceptedCount+rejectedCount)
			
			s.logger.Info("stream closed",
				slog.String("session_id", sessionID),
				slog.Int64("accepted", acceptedCount),
				slog.Int64("rejected", rejectedCount),
			)
			
			return stream.SendAndClose(&ingestorv1.IngestReadingsResponse{
				AcceptedCount: acceptedCount,
				RejectedCount: rejectedCount,
				SessionId:     sessionID,
				IngestedAt:    timestamppb.Now(),
			})
		}
		if err != nil {
			return status.Errorf(codes.Internal, "failed to receive: %v", err)
		}
		
		// Validate
		if err := s.validateReading(req.SensorReading); err != nil {
			s.readingsRejected.Add(ctx, 1)
			atomic.AddInt64(&rejectedCount, 1)
			s.logger.Warn("rejected reading",
				slog.String("sensor_id", req.SensorReading.SensorId),
				slog.String("error", err.Error()),
			)
			continue
		}
		
		// Add to batch
		batchMutex.Lock()
		batch = append(batch, req.SensorReading)
		
		// Flush if batch reaches 100
		if len(batch) >= 100 {
			s.flushBatch(ctx, batch, &acceptedCount, &rejectedCount)
			batch = batch[:0]
		}
		batchMutex.Unlock()
	}
}

// flushBatch saves the batch and publishes to NATS
func (s *IngestorService) flushBatch(ctx context.Context, batch []*commonv1.SensorReading, acceptedCount, rejectedCount *int64) {
	if len(batch) == 0 {
		return
	}
	
	// Save to database
	savedCount, err := s.repo.SaveReadingsBatch(ctx, batch)
	if err != nil {
		s.logger.Error("failed to save batch", slog.String("error", err.Error()))
		atomic.AddInt64(rejectedCount, int64(len(batch)))
		s.readingsRejected.Add(ctx, int64(len(batch)))
		return
	}
	
	// Publish to NATS
	for _, reading := range batch {
		if err := s.publisher.PublishReading(ctx, reading); err != nil {
			s.logger.Error("failed to publish reading", 
				slog.String("sensor_id", reading.SensorId),
				slog.String("error", err.Error()))
		}
	}
	
	atomic.AddInt64(acceptedCount, savedCount)
	s.readingsAccepted.Add(ctx, savedCount)
}

// IngestSingleReading handles a single reading (unary)
func (s *IngestorService) IngestSingleReading(ctx context.Context, req *ingestorv1.IngestSingleReadingRequest) (*ingestorv1.IngestSingleReadingResponse, error) {
	// Validate
	if err := s.validateReading(req.SensorReading); err != nil {
		s.readingsRejected.Add(ctx, 1)
		return &ingestorv1.IngestSingleReadingResponse{
			Accepted:         false,
			RejectionReason:  err.Error(),
		}, nil
	}
	
	// Save to database
	readingID, err := s.repo.SaveReading(ctx, req.SensorReading)
	if err != nil {
		s.readingsRejected.Add(ctx, 1)
		return nil, status.Errorf(codes.Internal, "failed to save reading: %v", err)
	}
	
	// Publish to NATS
	if err := s.publisher.PublishReading(ctx, req.SensorReading); err != nil {
		s.logger.Error("failed to publish reading", slog.String("error", err.Error()))
	}
	
	s.readingsAccepted.Add(ctx, 1)
	
	return &ingestorv1.IngestSingleReadingResponse{
		ReadingId: readingID,
		Accepted:  true,
	}, nil
}

// GetSensorStatus returns the current status of a sensor
func (s *IngestorService) GetSensorStatus(ctx context.Context, req *ingestorv1.GetSensorStatusRequest) (*ingestorv1.GetSensorStatusResponse, error) {
	if req.SensorId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "sensor_id is required")
	}
	
	status, err := s.repo.GetSensorLastReading(ctx, req.SensorId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "sensor %s not found", req.SensorId)
	}
	
	return &ingestorv1.GetSensorStatusResponse{
		SensorId:            status.SensorID,
		IsActive:            status.LastSeenAt.After(time.Now().Add(-5 * time.Minute)),
		LastSeenAt:          timestamppb.New(status.LastSeenAt),
		TotalReadingsToday:  status.TotalReadingsToday,
		LastDecibelLevel:    status.LastDecibelLevel,
	}, nil
}
