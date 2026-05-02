package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	alertv1 "github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/soundmap/soundmap/services/alert-engine/internal/engine"
	"github.com/soundmap/soundmap/services/alert-engine/internal/repository"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MockThresholdRepository for testing
type MockThresholdRepository struct {
	CreateThresholdFunc     func(ctx context.Context, t *alertv1.Threshold) error
	ListThresholdsFunc      func(ctx context.Context, page, pageSize int) ([]*alertv1.Threshold, int64, error)
	GetThresholdByIDFunc    func(ctx context.Context, id string) (*alertv1.Threshold, error)
	GetActiveThresholdsFunc func(ctx context.Context) ([]*alertv1.Threshold, error)
}

func (m *MockThresholdRepository) CreateThreshold(ctx context.Context, t *alertv1.Threshold) error {
	if m.CreateThresholdFunc != nil {
		return m.CreateThresholdFunc(ctx, t)
	}
	return nil
}

func (m *MockThresholdRepository) ListThresholds(ctx context.Context, page, pageSize int) ([]*alertv1.Threshold, int64, error) {
	if m.ListThresholdsFunc != nil {
		return m.ListThresholdsFunc(ctx, page, pageSize)
	}
	return nil, 0, nil
}

func (m *MockThresholdRepository) GetThresholdByID(ctx context.Context, id string) (*alertv1.Threshold, error) {
	if m.GetThresholdByIDFunc != nil {
		return m.GetThresholdByIDFunc(ctx, id)
	}
	return nil, errors.New("not found")
}

func (m *MockThresholdRepository) GetActiveThresholds(ctx context.Context) ([]*alertv1.Threshold, error) {
	if m.GetActiveThresholdsFunc != nil {
		return m.GetActiveThresholdsFunc(ctx)
	}
	return nil, nil
}

// MockAlertRepository for testing
type MockAlertRepository struct {
	SaveAlertFunc               func(ctx context.Context, alert *repository.FiredAlert) error
	AcknowledgeAlertFunc        func(ctx context.Context, alertID, acknowledgedBy string) (*repository.FiredAlert, error)
	GetUnacknowledgedAlertsFunc func(ctx context.Context) ([]*repository.FiredAlert, error)
}

func (m *MockAlertRepository) SaveAlert(ctx context.Context, alert *repository.FiredAlert) error {
	if m.SaveAlertFunc != nil {
		return m.SaveAlertFunc(ctx, alert)
	}
	return nil
}

func (m *MockAlertRepository) AcknowledgeAlert(ctx context.Context, alertID, acknowledgedBy string) (*repository.FiredAlert, error) {
	if m.AcknowledgeAlertFunc != nil {
		return m.AcknowledgeAlertFunc(ctx, alertID, acknowledgedBy)
	}
	return nil, errors.New("not found")
}

func (m *MockAlertRepository) GetUnacknowledgedAlerts(ctx context.Context) ([]*repository.FiredAlert, error) {
	if m.GetUnacknowledgedAlertsFunc != nil {
		return m.GetUnacknowledgedAlertsFunc(ctx)
	}
	return nil, nil
}

func createAlertService(thresholdRepo repository.ThresholdRepository, alertRepo repository.AlertRepository) (*AlertService, *engine.AlertEngine, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	meter := noop.MeterProvider{}.Meter("test")

	eng, err := engine.NewAlertEngine(thresholdRepo, alertRepo, logger, meter)
	if err != nil {
		return nil, nil, err
	}

	svc, err := NewAlertService(eng, thresholdRepo, alertRepo, logger, meter)
	if err != nil {
		return nil, nil, err
	}

	return svc, eng, nil
}

func TestCreateThreshold_ValidThreshold(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{
		CreateThresholdFunc: func(ctx context.Context, t *alertv1.Threshold) error {
			return nil
		},
	}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.CreateThresholdRequest{
		Name:       "High Noise Threshold",
		H3Index:    "8928308280fffff",
		MaxDecibel: 85.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	resp, err := svc.CreateThreshold(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.Threshold.ThresholdId)
	assert.Equal(t, "High Noise Threshold", resp.Threshold.Name)
	assert.Equal(t, 85.0, resp.Threshold.MaxDecibel)
	assert.Equal(t, alertv1.AlertSeverity_ALERT_SEVERITY_WARNING, resp.Threshold.Severity)
	assert.True(t, resp.Threshold.IsActive)
}

func TestCreateThreshold_EmptyName(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.CreateThresholdRequest{
		Name:       "",
		MaxDecibel: 85.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	_, err = svc.CreateThreshold(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "name is required")
}

func TestCreateThreshold_DecibelOutOfRange(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	// Test too low
	req := &alertv1.CreateThresholdRequest{
		Name:       "Low Threshold",
		MaxDecibel: 5.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	_, err = svc.CreateThreshold(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Test too high
	req = &alertv1.CreateThresholdRequest{
		Name:       "High Threshold",
		MaxDecibel: 250.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	_, err = svc.CreateThreshold(context.Background(), req)
	assert.Error(t, err)
	st, ok = status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateThreshold_InvalidH3Index(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.CreateThresholdRequest{
		Name:       "Zone Threshold",
		H3Index:    "invalid",
		MaxDecibel: 85.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	_, err = svc.CreateThreshold(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid h3_index")
}

func TestCreateThreshold_NoSeverity(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.CreateThresholdRequest{
		Name:       "Threshold",
		MaxDecibel: 85.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED,
	}

	_, err = svc.CreateThreshold(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "severity is required")
}

func TestAcknowledgeAlert_Valid(t *testing.T) {
	acknowledgedAt := time.Now()
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{
		AcknowledgeAlertFunc: func(ctx context.Context, alertID, acknowledgedBy string) (*repository.FiredAlert, error) {
			return &repository.FiredAlert{
				AlertID:        alertID,
				AcknowledgedAt: &acknowledgedAt,
				AcknowledgedBy: acknowledgedBy,
			}, nil
		},
	}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.AcknowledgeAlertRequest{
		AlertId:        "alert-001",
		AcknowledgedBy: "admin",
	}

	resp, err := svc.AcknowledgeAlert(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "alert-001", resp.AlertId)
	assert.NotNil(t, resp.AcknowledgedAt)
}

func TestAcknowledgeAlert_MissingAlertID(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.AcknowledgeAlertRequest{
		AlertId:        "",
		AcknowledgedBy: "admin",
	}

	_, err = svc.AcknowledgeAlert(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAcknowledgeAlert_AlertNotFound(t *testing.T) {
	thresholdRepo := &MockThresholdRepository{}
	alertRepo := &MockAlertRepository{
		AcknowledgeAlertFunc: func(ctx context.Context, alertID, acknowledgedBy string) (*repository.FiredAlert, error) {
			return nil, errors.New("not found")
		},
	}
	svc, _, err := createAlertService(thresholdRepo, alertRepo)
	assert.NoError(t, err)

	req := &alertv1.AcknowledgeAlertRequest{
		AlertId:        "non-existent",
		AcknowledgedBy: "admin",
	}

	_, err = svc.AcknowledgeAlert(context.Background(), req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestSeverityMeetsMin(t *testing.T) {
	tests := []struct {
		severity alertv1.AlertSeverity
		min      alertv1.AlertSeverity
		expected bool
	}{
		{alertv1.AlertSeverity_ALERT_SEVERITY_INFO, alertv1.AlertSeverity_ALERT_SEVERITY_INFO, true},
		{alertv1.AlertSeverity_ALERT_SEVERITY_WARNING, alertv1.AlertSeverity_ALERT_SEVERITY_INFO, true},
		{alertv1.AlertSeverity_ALERT_SEVERITY_CRITICAL, alertv1.AlertSeverity_ALERT_SEVERITY_WARNING, true},
		{alertv1.AlertSeverity_ALERT_SEVERITY_INFO, alertv1.AlertSeverity_ALERT_SEVERITY_WARNING, false},
		{alertv1.AlertSeverity_ALERT_SEVERITY_UNSPECIFIED, alertv1.AlertSeverity_ALERT_SEVERITY_INFO, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_meets_%s", tt.severity.String(), tt.min.String()), func(t *testing.T) {
			result := severityMeetsMin(tt.severity, tt.min)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestH3IndexAllowed(t *testing.T) {
	tests := []struct {
		h3Index  string
		allowed  []string
		expected bool
	}{
		{"8928308280fffff", []string{"8928308280fffff"}, true},
		{"8928308280fffff", []string{"8928308280fffff", "8928308281fffff"}, true},
		{"8928308280fffff", []string{"8928308281fffff"}, false},
		{"8928308280fffff", []string{}, true}, // Empty allowed list means all zones
		{"8928308280fffff", nil, true},        // Nil allowed list means all zones
	}

	for _, tt := range tests {
		t.Run(tt.h3Index, func(t *testing.T) {
			result := h3IndexAllowed(tt.h3Index, tt.allowed)
			assert.Equal(t, tt.expected, result)
		})
	}
}
