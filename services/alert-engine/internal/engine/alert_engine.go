package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	alertv1 "github.com/soundmap/soundmap/gen/go/alert/v1"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/soundmap/soundmap/services/alert-engine/internal/repository"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AlertEngine is the core alert processing engine
type AlertEngine struct {
	thresholdRepo ThresholdRepository
	alertRepo     repository.AlertRepository
	subscribers   map[string]chan *alertv1.AlertEvent
	mu            sync.RWMutex
	thresholds    []*alertv1.Threshold
	logger        *slog.Logger
	alertsFired   metric.Int64Counter
}

// ThresholdRepository interface for threshold operations
type ThresholdRepository interface {
	GetActiveThresholds(ctx context.Context) ([]*alertv1.Threshold, error)
}

// NewAlertEngine creates a new alert engine
func NewAlertEngine(thresholdRepo ThresholdRepository, alertRepo repository.AlertRepository, logger *slog.Logger, meter metric.Meter) (*AlertEngine, error) {
	alertsFired, err := meter.Int64Counter("alert_engine_alerts_fired_total", metric.WithDescription("Total number of alerts fired"))
	if err != nil {
		return nil, fmt.Errorf("failed to create alerts_fired counter: %w", err)
	}

	return &AlertEngine{
		thresholdRepo: thresholdRepo,
		alertRepo:     alertRepo,
		subscribers:   make(map[string]chan *alertv1.AlertEvent),
		logger:        logger,
		alertsFired:   alertsFired,
	}, nil
}

// Start runs the alert engine
func (e *AlertEngine) Start(ctx context.Context) error {
	// Initial threshold load
	if err := e.refreshThresholds(ctx); err != nil {
		e.logger.Error("failed to load initial thresholds", slog.String("error", err.Error()))
	}

	// Start threshold refresh ticker
	refreshTicker := time.NewTicker(30 * time.Second)
	defer refreshTicker.Stop()

	// Start heartbeat ticker
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("alert engine shutting down")
			return nil
		case <-refreshTicker.C:
			if err := e.refreshThresholds(ctx); err != nil {
				e.logger.Error("failed to refresh thresholds", slog.String("error", err.Error()))
			}
		case <-heartbeatTicker.C:
			e.sendHeartbeat()
		}
	}
}

// refreshThresholds loads active thresholds from the database
func (e *AlertEngine) refreshThresholds(ctx context.Context) error {
	thresholds, err := e.thresholdRepo.GetActiveThresholds(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active thresholds: %w", err)
	}

	e.mu.Lock()
	e.thresholds = thresholds
	e.mu.Unlock()

	e.logger.Info("thresholds refreshed", slog.Int("count", len(thresholds)))
	return nil
}

// sendHeartbeat broadcasts a heartbeat to all subscribers
func (e *AlertEngine) sendHeartbeat() {
	heartbeatEvent := &alertv1.AlertEvent{
		AlertId:     uuid.New().String(),
		Severity:    alertv1.AlertSeverity_ALERT_SEVERITY_INFO,
		FiredAt:     timestamppb.Now(),
		IsHeartbeat: true,
		Message:     "Heartbeat - Alert Engine is healthy",
	}

	e.broadcastAlert(heartbeatEvent)
}

// ProcessReading processes a sensor reading and fires alerts if thresholds are breached
func (e *AlertEngine) ProcessReading(ctx context.Context, sensorID, h3Index string, decibelLevel float64) error {
	if decibelLevel <= 0 {
		return nil // No alert needed for invalid readings
	}

	e.mu.RLock()
	thresholds := e.thresholds
	e.mu.RUnlock()

	for _, threshold := range thresholds {
		// Check if threshold applies to this zone (empty h3_index means "all zones")
		if threshold.H3Index != "" && threshold.H3Index != h3Index {
			continue
		}

		// Check if decibel level exceeds threshold
		if decibelLevel > threshold.MaxDecibel {
			// Fire alert
			if err := e.fireAlert(ctx, threshold, sensorID, h3Index, decibelLevel); err != nil {
				e.logger.Error("failed to fire alert",
					slog.String("threshold_id", threshold.ThresholdId),
					slog.String("sensor_id", sensorID),
					slog.String("error", err.Error()))
				continue
			}
		}
	}

	return nil
}

// fireAlert creates and broadcasts an alert
func (e *AlertEngine) fireAlert(ctx context.Context, threshold *alertv1.Threshold, sensorID, h3Index string, decibelLevel float64) error {
	// Create alert message
	message := fmt.Sprintf("Decibel level %.1f dB exceeds threshold %.1f dB in zone %s",
		decibelLevel, threshold.MaxDecibel, h3Index)

	alertEvent := &alertv1.AlertEvent{
		AlertId:           uuid.New().String(),
		ThresholdId:       threshold.ThresholdId,
		SensorId:          sensorID,
		H3Index:           h3Index,
		Severity:          threshold.Severity,
		CurrentDecibel:    decibelLevel,
		ThresholdDecibel:  threshold.MaxDecibel,
		Message:           message,
		FiredAt:           timestamppb.Now(),
		IsHeartbeat:       false,
	}

	// Save to database
	firedAlert := &repository.FiredAlert{
		AlertID:          alertEvent.AlertId,
		ThresholdID:      threshold.ThresholdId,
		SensorID:         sensorID,
		H3Index:          h3Index,
		Severity:         threshold.Severity.String(),
		CurrentDecibel:   decibelLevel,
		ThresholdDecibel: threshold.MaxDecibel,
		Message:          message,
		FiredAt:          time.Now(),
	}

	if err := e.alertRepo.SaveAlert(ctx, firedAlert); err != nil {
		return fmt.Errorf("failed to save alert: %w", err)
	}

	// Broadcast to subscribers
	e.broadcastAlert(alertEvent)

	// Increment metric
	e.alertsFired.Add(ctx, 1)

	e.logger.Info("alert fired",
		slog.String("alert_id", alertEvent.AlertId),
		slog.String("threshold_id", threshold.ThresholdId),
		slog.String("sensor_id", sensorID),
		slog.Float64("decibel", decibelLevel))

	return nil
}

// broadcastAlert sends an alert to all subscribers (non-blocking)
func (e *AlertEngine) broadcastAlert(event *alertv1.AlertEvent) {
	e.mu.RLock()
	subscribers := make(map[string]chan *alertv1.AlertEvent, len(e.subscribers))
	for k, v := range e.subscribers {
		subscribers[k] = v
	}
	e.mu.RUnlock()

	for id, ch := range subscribers {
		select {
		case ch <- event:
			// Successfully sent
		default:
			// Channel is full, skip this subscriber
			e.logger.Warn("subscriber channel full, skipping",
				slog.String("subscriber_id", id))
		}
	}
}

// Subscribe creates a new subscriber and returns the channel and unsubscribe function
func (e *AlertEngine) Subscribe(subscriberID string) (<-chan *alertv1.AlertEvent, func()) {
	ch := make(chan *alertv1.AlertEvent, 100)

	e.mu.Lock()
	e.subscribers[subscriberID] = ch
	e.mu.Unlock()

	e.logger.Info("subscriber added", slog.String("subscriber_id", subscriberID))

	unsubscribe := func() {
		e.mu.Lock()
		if ch, exists := e.subscribers[subscriberID]; exists {
			close(ch)
			delete(e.subscribers, subscriberID)
		}
		e.mu.Unlock()
		e.logger.Info("subscriber removed", slog.String("subscriber_id", subscriberID))
	}

	return ch, unsubscribe
}

// Helper function to check if severity meets minimum
func severityMeetsMin(severity, minSeverity alertv1.AlertSeverity) bool {
	// Define severity order: UNSPECIFIED < INFO < WARNING < CRITICAL
	severityOrder := map[alertv1.AlertSeverity]int{
		alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED: 0,
		alertv1.AlertSeverity_ALERT_SEVERITY_INFO:         1,
		alertv1.AlertSeverity_ALERT_SEVERITY_WARNING:       2,
		alertv1.AlertSeverity_ALERT_SEVERITY_CRITICAL:      3,
	}

	return severityOrder[severity] >= severityOrder[minSeverity]
}

// Helper function to check if H3 index is in the allowed list
func h3IndexAllowed(h3Index string, allowed []string) bool {
	if len(allowed) == 0 {
		return true // Empty list means all zones allowed
	}
	for _, allowedIndex := range allowed {
		if h3Index == allowedIndex {
			return true
		}
	}
	return false
}

// GetActiveThresholds returns the current cached thresholds (for testing)
func (e *AlertEngine) GetActiveThresholds() []*alertv1.Threshold {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.thresholds
}
