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

// ReadingConsumer defines the interface for consuming sensor readings
type ReadingConsumer interface {
	Start(ctx context.Context, handler func(ctx context.Context, reading *commonv1.SensorReading) error) error
	Close() error
}

// NATSConsumer implements ReadingConsumer using NATS JetStream
type NATSConsumer struct {
	conn         *nats.Conn
	js           nats.JetStreamContext
	subscription *nats.Subscription
	streamName   string
	consumerName string
	logger       *slog.Logger
	subject      string
}

const (
	streamName   = "SOUNDMAP_READINGS"
	subject      = "readings.raw"
	consumerName = "analyzer-consumer"
)

// NewNATSConsumer creates a new NATS JetStream consumer
func NewNATSConsumer(natsURL string, logger *slog.Logger) (*NATSConsumer, error) {
	// Connect to NATS with auto-reconnect settings
	conn, err := nats.Connect(natsURL,
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Error("NATS disconnected", slog.String("error", err.Error()))
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info("NATS reconnected", slog.String("url", nc.ConnectedUrl()))
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create JetStream context
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	return &NATSConsumer{
		conn:         conn,
		js:           js,
		streamName:   streamName,
		consumerName: consumerName,
		logger:       logger,
		subject:      subject,
	}, nil
}

// Start begins consuming messages from NATS
func (c *NATSConsumer) Start(ctx context.Context, handler func(ctx context.Context, reading *commonv1.SensorReading) error) error {
	// Create durable consumer
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

			c.logger.Debug("received message",
				slog.String("subject", msg.Subject),
				slog.String("sensor_id", msg.Header.Get("sensor_id")),
			)

			// Deserialize
			var reading commonv1.SensorReading
			if err := proto.Unmarshal(msg.Data, &reading); err != nil {
				c.logger.Error("failed to unmarshal reading",
					slog.String("error", err.Error()))
				msg.Nak()
				return
			}

			// Process
			if err := handler(ctx, &reading); err != nil {
				c.logger.Error("handler error",
					slog.String("sensor_id", reading.SensorId),
					slog.String("error", err.Error()))
				msg.Nak()
				return
			}

			// Acknowledge
			if err := msg.Ack(); err != nil {
				c.logger.Error("failed to ack message",
					slog.String("error", err.Error()))
			}
		},
		nats.Durable(c.consumerName),
		nats.ManualAck(),
		nats.DeliverAll(),
		nats.MaxDeliver(3),
	)
	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	c.subscription = sub
	c.logger.Info("started NATS consumer",
		slog.String("subject", c.subject),
		slog.String("consumer", c.consumerName))

	// Wait for context cancellation
	<-ctx.Done()
	return nil
}

// Close closes the NATS consumer
func (c *NATSConsumer) Close() error {
	if c.subscription != nil {
		c.subscription.Unsubscribe()
	}
	c.conn.Close()
	return nil
}
