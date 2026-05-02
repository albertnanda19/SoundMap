package consumer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"google.golang.org/protobuf/proto"
)

// AlertEngine interface for processing readings
type AlertEngine interface {
	ProcessReading(ctx context.Context, sensorID, h3Index string, decibelLevel float64) error
}

// H3Util interface for geospatial operations
type H3Util interface {
	LatLngToH3Index(lat, lng float64, resolution int32) string
}

// AnalysisConsumer consumes analysis results from NATS
type AnalysisConsumer struct {
	conn        *nats.Conn
	js          nats.JetStreamContext
	subject     string
	consumer    string
	logger      *slog.Logger
	alertEngine AlertEngine
	h3Util      H3Util
}

const (
	subjectAnalyzed = "readings.analyzed"
	consumerName    = "alert-engine-consumer"
)

// NewAnalysisConsumer creates a new NATS consumer for analysis results
func NewAnalysisConsumer(natsURL string, logger *slog.Logger, alertEngine AlertEngine, h3Util H3Util) (*AnalysisConsumer, error) {
	conn, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	return &AnalysisConsumer{
		conn:        conn,
		js:          js,
		subject:     subjectAnalyzed,
		consumer:    consumerName,
		logger:      logger,
		alertEngine: alertEngine,
		h3Util:      h3Util,
	}, nil
}

// Start begins consuming messages from NATS
func (c *AnalysisConsumer) Start(ctx context.Context) error {
	// Ensure stream exists
	_, err := c.js.AddStream(&nats.StreamConfig{
		Name:     "SOUNDMAP_ANALYZED",
		Subjects: []string{subjectAnalyzed},
		Storage:  nats.MemoryStorage,
		MaxAge:   24 * time.Hour,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("failed to create stream: %w", err)
	}

	sub, err := c.js.Subscribe(
		c.subject,
		func(msg *nats.Msg) {
			// Check context cancellation
			select {
			case <-ctx.Done():
				msg.Nak()
				return
			default:
			}

			c.logger.Debug("received analyzed reading",
				slog.String("subject", msg.Subject))

			// Deserialize the analysis result
			var reading commonv1.SensorReading
			if err := proto.Unmarshal(msg.Data, &reading); err != nil {
				c.logger.Error("failed to unmarshal reading",
					slog.String("error", err.Error()))
				msg.Nak()
				return
			}

			// Get H3 index for the reading location
			h3Index := c.h3Util.LatLngToH3Index(reading.Latitude, reading.Longitude, 9)

			// Process the reading through the alert engine
			if err := c.alertEngine.ProcessReading(ctx, reading.SensorId, h3Index, reading.DecibelLevel); err != nil {
				c.logger.Error("alert engine processing failed",
					slog.String("sensor_id", reading.SensorId),
					slog.String("error", err.Error()))
				msg.Nak()
				return
			}

			// Acknowledge the message
			if err := msg.Ack(); err != nil {
				c.logger.Error("failed to ack message",
					slog.String("error", err.Error()))
			}
		},
		nats.Durable(c.consumer),
		nats.ManualAck(),
		nats.DeliverAll(),
		nats.MaxDeliver(3),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.logger.Info("started analysis consumer",
		slog.String("subject", c.subject),
		slog.String("consumer", c.consumer))

	// Wait for context cancellation
	<-ctx.Done()
	sub.Unsubscribe()
	return nil
}

// Close closes the NATS connection
func (c *AnalysisConsumer) Close() error {
	c.conn.Close()
	return nil
}
