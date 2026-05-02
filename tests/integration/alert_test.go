package integration

import (
	"context"
	"testing"
	"time"

	"github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateThreshold_Valid(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	req := &alertv1.CreateThresholdRequest{
		Name:       "Test Threshold",
		H3Index:    "8928308280fffff",
		MaxDecibel: 85.0,
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	resp, err := alertClient.CreateThreshold(ctx, req)
	assert.NoError(t, err)
	assert.NotEmpty(t, resp.Threshold.ThresholdId)
	assert.True(t, resp.Threshold.IsActive)
	assert.Equal(t, "Test Threshold", resp.Threshold.Name)

	// Cleanup
	t.Cleanup(func() {
		// Could add cleanup logic here if API supports delete
	})
}

func TestCreateThreshold_InvalidDecibel(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	req := &alertv1.CreateThresholdRequest{
		Name:       "Invalid Threshold",
		MaxDecibel: 300.0, // Too high
		Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
	}

	_, err := alertClient.CreateThreshold(ctx, req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListThresholds(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	// Create 2 thresholds
	for i := 0; i < 2; i++ {
		_, _ = alertClient.CreateThreshold(ctx, &alertv1.CreateThresholdRequest{
			Name:       "List Test " + string(rune('0'+i)),
			MaxDecibel: 80.0 + float64(i)*5,
			Severity:   alertv1.AlertSeverity_ALERT_SEVERITY_WARNING,
		})
	}

	resp, err := alertClient.ListThresholds(ctx, &alertv1.ListThresholdsRequest{
		Page: &alertv1.Pagination{
			Page:     1,
			PageSize: 100,
		},
	})
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(resp.Thresholds), 2) // May include seeded defaults
}

func TestSubscribeAlerts_ReceivesHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	// Use 45s timeout for heartbeat (sent every 30s)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	stream, err := alertClient.SubscribeAlerts(ctx, &alertv1.SubscribeAlertsRequest{
		SubscriberId: "test-sub-01",
	})
	assert.NoError(t, err)

	receivedHeartbeat := false
	for {
		resp, err := stream.Recv()
		if err != nil {
			break
		}
		if resp.IsHeartbeat {
			receivedHeartbeat = true
			break
		}
	}

	assert.True(t, receivedHeartbeat, "Should receive heartbeat within 45 seconds")
}
