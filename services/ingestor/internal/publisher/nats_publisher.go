package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"google.golang.org/protobuf/proto"
)

// ReadingPublisher defines the interface for publishing sensor readings
type ReadingPublisher interface {
	PublishReading(ctx context.Context, reading *commonv1.SensorReading) error
	PublishReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) error
	Close() error
}

// NATSPublisher implements ReadingPublisher using NATS JetStream
type NATSPublisher struct {
	conn       *nats.Conn
	js         nats.JetStreamContext
	streamName string
	subject    string
}

const (
	streamName = "SOUNDMAP_READINGS"
	subject    = "readings.raw"
)

// NewNATSPublisher creates a new NATS JetStream publisher
func NewNATSPublisher(natsURL string) (*NATSPublisher, error) {
	// Connect to NATS with auto-reconnect settings
	conn, err := nats.Connect(natsURL,
		nats.Timeout(10*time.Second),
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.ReconnectWait(2*time.Second),
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

	// Create or update stream
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      streamName,
		Subjects:  []string{subject},
		Storage:   nats.MemoryStorage,
		MaxAge:    24 * time.Hour,
		MaxMsgs:   1_000_000,
		Retention: nats.LimitsPolicy,
		Discard:   nats.DiscardOld,
	})
	if err != nil && err != nats.ErrStreamNameAlreadyInUse {
		conn.Close()
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	return &NATSPublisher{
		conn:       conn,
		js:         js,
		streamName: streamName,
		subject:    subject,
	}, nil
}

// PublishReading publishes a single sensor reading to NATS
func (p *NATSPublisher) PublishReading(ctx context.Context, reading *commonv1.SensorReading) error {
	// Serialize to protobuf
	data, err := proto.Marshal(reading)
	if err != nil {
		return fmt.Errorf("failed to marshal reading: %w", err)
	}

	// Create message with headers
	msg := &nats.Msg{
		Subject: p.subject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set("Content-Type", "application/protobuf")
	msg.Header.Set("sensor_id", reading.SensorId)

	// Publish with context
	_, err = p.js.PublishMsg(msg, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("failed to publish reading: %w", err)
	}

	return nil
}

// PublishReadingsBatch publishes multiple readings in batch
func (p *NATSPublisher) PublishReadingsBatch(ctx context.Context, readings []*commonv1.SensorReading) error {
	for _, reading := range readings {
		if err := p.PublishReading(ctx, reading); err != nil {
			return fmt.Errorf("failed to publish batch reading: %w", err)
		}
	}
	return nil
}

// Close closes the NATS connection
func (p *NATSPublisher) Close() error {
	p.conn.Close()
	return nil
}
