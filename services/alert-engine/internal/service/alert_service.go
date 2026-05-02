package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	alertv1 "github.com/soundmap/soundmap/gen/go/alert/v1"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/soundmap/soundmap/services/alert-engine/internal/engine"
	"github.com/soundmap/soundmap/services/alert-engine/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AlertService implements the alert gRPC service
type AlertService struct {
	engine        *engine.AlertEngine
	thresholdRepo repository.ThresholdRepository
	alertRepo     repository.AlertRepository
	logger        *slog.Logger
	meter         metric.Meter

	subscribersActive metric.Int64UpDownCounter
	alertsSentTotal   metric.Int64Counter
	thresholdsTotal   metric.Int64UpDownCounter
}

// NewAlertService creates a new alert service
func NewAlertService(eng *engine.AlertEngine, thresholdRepo repository.ThresholdRepository, alertRepo repository.AlertRepository, logger *slog.Logger, meter metric.Meter) (*AlertService, error) {
	var err error
	svc := &AlertService{
		engine:        eng,
		thresholdRepo: thresholdRepo,
		alertRepo:     alertRepo,
		logger:        logger,
		meter:         meter,
	}

	svc.subscribersActive, err = meter.Int64UpDownCounter("alert_subscribers_active", metric.WithDescription("Current number of active subscribers"))
	if err != nil {
		return nil, err
	}

	svc.alertsSentTotal, err = meter.Int64Counter("alert_events_sent_total", metric.WithDescription("Total number of alert events sent"))
	if err != nil {
		return nil, err
	}

	svc.thresholdsTotal, err = meter.Int64UpDownCounter("alert_thresholds_total", metric.WithDescription("Total number of alert thresholds"))
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// SubscribeAlerts handles server streaming for alert subscriptions
func (s *AlertService) SubscribeAlerts(req *alertv1.SubscribeAlertsRequest, stream alertv1.AlertService_SubscribeAlertsServer) error {
	ctx := stream.Context()

	// Validate subscriber_id
	if req.SubscriberId == "" {
		return status.Error(codes.InvalidArgument, "subscriber_id is required")
	}

	// Subscribe to the alert engine
	ch, unsubscribe := s.engine.Subscribe(req.SubscriberId)
	defer unsubscribe()

	// Increment subscriber metric
	s.subscribersActive.Add(ctx, 1)
	defer s.subscribersActive.Add(ctx, -1)

	s.logger.Info("subscriber connected", slog.String("subscriber_id", req.SubscriberId))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("subscriber disconnected", slog.String("subscriber_id", req.SubscriberId))
			return status.Error(codes.Canceled, "subscriber disconnected")

		case event, ok := <-ch:
			if !ok {
				// Channel closed
				s.logger.Info("subscription channel closed", slog.String("subscriber_id", req.SubscriberId))
				return nil
			}

			// Apply severity filter
			if req.MinSeverity != alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED {
				if !severityMeetsMin(event.Severity, req.MinSeverity) {
					continue
				}
			}

			// Apply H3 index filter
			if len(req.H3Indexes) > 0 {
				if !h3IndexAllowed(event.H3Index, req.H3Indexes) {
					continue
				}
			}

			// Send event
			if err := stream.Send(event); err != nil {
				s.logger.Error("failed to send alert event",
					slog.String("subscriber_id", req.SubscriberId),
					slog.String("error", err.Error()))
				return status.Errorf(codes.Internal, "failed to send event: %v", err)
			}

			s.alertsSentTotal.Add(ctx, 1)
		}
	}
}

// CreateThreshold creates a new alert threshold
func (s *AlertService) CreateThreshold(ctx context.Context, req *alertv1.CreateThresholdRequest) (*alertv1.CreateThresholdResponse, error) {
	// Validate name
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Validate max_decibel
	if req.MaxDecibel < 10 || req.MaxDecibel > 200 {
		return nil, status.Error(codes.InvalidArgument, "max_decibel must be between 10 and 200")
	}

	// Validate severity
	if req.Severity == alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "severity is required")
	}

	// Validate H3 index if provided (empty is allowed for "all zones")
	if req.H3Index != "" {
		// In production, use h3util.ValidateH3Index(req.H3Index)
		if len(req.H3Index) != 15 {
			return nil, status.Error(codes.InvalidArgument, "invalid h3_index format")
		}
	}

	// Build threshold
	threshold := &alertv1.Threshold{
		ThresholdId: uuid.New().String(),
		Name:        req.Name,
		H3Index:     req.H3Index,
		MaxDecibel:  req.MaxDecibel,
		Severity:    req.Severity,
		IsActive:    true,
		CreatedAt:   timestamppb.Now(),
	}

	// Save to repository
	if err := s.thresholdRepo.CreateThreshold(ctx, threshold); err != nil {
		s.logger.Error("failed to create threshold", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to create threshold: %v", err)
	}

	s.thresholdsTotal.Add(ctx, 1)

	s.logger.Info("threshold created",
		slog.String("threshold_id", threshold.ThresholdId),
		slog.String("name", threshold.Name))

	return &alertv1.CreateThresholdResponse{
		Threshold: threshold,
	}, nil
}

// ListThresholds returns a paginated list of thresholds
func (s *AlertService) ListThresholds(ctx context.Context, req *alertv1.ListThresholdsRequest) (*alertv1.ListThresholdsResponse, error) {
	// Parse pagination
	page := int(req.Page.Page)
	pageSize := int(req.Page.PageSize)

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Query repository
	thresholds, total, err := s.thresholdRepo.ListThresholds(ctx, page, pageSize)
	if err != nil {
		s.logger.Error("failed to list thresholds", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.Internal, "failed to list thresholds: %v", err)
	}

	return &alertv1.ListThresholdsResponse{
		Thresholds: thresholds,
		Pagination: &commonv1.Pagination{
			Page:     int32(page),
			PageSize: int32(pageSize),
			Total:    total,
		},
	}, nil
}

// AcknowledgeAlert acknowledges a fired alert
func (s *AlertService) AcknowledgeAlert(ctx context.Context, req *alertv1.AcknowledgeAlertRequest) (*alertv1.AcknowledgeAlertResponse, error) {
	// Validate
	if req.AlertId == "" {
		return nil, status.Error(codes.InvalidArgument, "alert_id is required")
	}
	if req.AcknowledgedBy == "" {
		return nil, status.Error(codes.InvalidArgument, "acknowledged_by is required")
	}

	// Acknowledge in repository
	alert, err := s.alertRepo.AcknowledgeAlert(ctx, req.AlertId, req.AcknowledgedBy)
	if err != nil {
		s.logger.Error("failed to acknowledge alert", slog.String("error", err.Error()))
		return nil, status.Errorf(codes.NotFound, "alert not found or already acknowledged")
	}

	s.logger.Info("alert acknowledged",
		slog.String("alert_id", alert.AlertID),
		slog.String("acknowledged_by", alert.AcknowledgedBy))

	return &alertv1.AcknowledgeAlertResponse{
		AlertId:        alert.AlertID,
		AcknowledgedAt: timestamppb.New(*alert.AcknowledgedAt),
	}, nil
}

// severityMeetsMin checks if severity meets minimum requirement
func severityMeetsMin(severity, minSeverity alertv1.AlertSeverity) bool {
	severityOrder := map[alertv1.AlertSeverity]int{
		alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED: 0,
		alertv1.AlertSeverity_ALERT_SEVERITY_INFO:        1,
		alertv1.AlertSeverity_ALERT_SEVERITY_WARNING:     2,
		alertv1.AlertSeverity_ALERT_SEVERITY_CRITICAL:    3,
	}

	return severityOrder[severity] >= severityOrder[minSeverity]
}

// h3IndexAllowed checks if H3 index is in the allowed list
func h3IndexAllowed(h3Index string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedIndex := range allowed {
		if h3Index == allowedIndex {
			return true
		}
	}
	return false
}
