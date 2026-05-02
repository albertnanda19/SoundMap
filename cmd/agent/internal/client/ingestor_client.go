package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/soundmap/soundmap/cmd/agent/internal/simulator"
	"github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// IngestorClient defines the interface for sending readings to the ingestor
type IngestorClient interface {
	StreamReadings(ctx context.Context, sensors []*simulator.Sensor, intervalMs int) error
	Close() error
}

// GRPCIngestorClient implements IngestorClient using gRPC streaming
type GRPCIngestorClient struct {
	conn      *grpc.ClientConn
	client    ingestorv1.IngestorServiceClient
	jwtToken  string
	logger    *slog.Logger
}

// NewGRPCIngestorClient creates a new gRPC client for the ingestor service
func NewGRPCIngestorClient(addr, jwtToken string, tlsEnabled bool, certFiles ...string) (*GRPCIngestorClient, error) {
	var creds credentials.TransportCredentials
	if tlsEnabled {
		creds = insecure.NewCredentials() // In production, load proper TLS certs
	} else {
		creds = insecure.NewCredentials()
	}

	// Auth interceptor adds JWT token to metadata
	authInterceptor := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if jwtToken != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+jwtToken)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}

	// Stream interceptor for auth
	streamAuthInterceptor := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if jwtToken != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+jwtToken)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithUnaryInterceptor(authInterceptor),
		grpc.WithStreamInterceptor(streamAuthInterceptor),
	}

	conn, err := grpc.NewClient(addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection: %w", err)
	}

	return &GRPCIngestorClient{
		conn:     conn,
		client:   ingestorv1.NewIngestorServiceClient(conn),
		jwtToken: jwtToken,
		logger:   slog.Default(),
	}, nil
}

// StreamReadings sends sensor readings to the ingestor using a single client-streaming RPC
func (c *GRPCIngestorClient) StreamReadings(ctx context.Context, sensors []*simulator.Sensor, intervalMs int) error {
	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	sentCount := 0
	startTime := time.Now()

	for {
		// Create a new stream for each connection attempt
		err := c.sendBatch(ctx, sensors, ticker, &sentCount, startTime)
		if err == nil {
			// Normal shutdown
			return nil
		}

		// Check if context is cancelled
		if ctx.Err() != nil {
			c.logger.Info("StreamReadings cancelled", slog.String("error", ctx.Err().Error()))
			return ctx.Err()
		}

		// Handle reconnection for transient errors
		if isRetryableError(err) {
			c.logger.Warn("Stream broken, attempting reconnection", slog.String("error", err.Error()))
			time.Sleep(2 * time.Second)
			continue
		}

		// Non-retryable error
		return err
	}
}

func (c *GRPCIngestorClient) sendBatch(ctx context.Context, sensors []*simulator.Sensor, ticker *time.Ticker, sentCount *int, startTime time.Time) error {
	// Open client-streaming RPC
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stream, err := c.client.IngestReadings(streamCtx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown - close stream and get summary
			resp, err := stream.CloseAndRecv()
			if err != nil {
				c.logger.Warn("Failed to close stream gracefully", slog.String("error", err.Error()))
			} else {
				c.logger.Info("Stream closed",
					slog.Int64("readings_received", resp.ReadingsReceived),
					slog.Int64("readings_stored", resp.ReadingsStored),
					slog.Int64("readings_published", resp.ReadingsPublished))
			}
			return nil

		case <-ticker.C:
			now := time.Now()
			for _, sensor := range sensors {
				reading := sensor.GenerateReading(now)
				if reading == nil {
					continue // Sensor inactive
				}

				req := &ingestorv1.IngestSingleReadingRequest{
					Reading: reading,
				}

				if err := stream.Send(req); err != nil {
					return fmt.Errorf("failed to send reading: %w", err)
				}

				*sentCount++
			}
		}
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific gRPC error codes
	errCode := status.Code(err)
	switch errCode {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return true
	default:
		return false
	}
}

// Close closes the gRPC connection
func (c *GRPCIngestorClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// status.Code extracts the gRPC status code from an error
func status(err error) *statusStatus {
	return &statusStatus{code: codes.Unknown}
}

type statusStatus struct {
	code codes.Code
}

func (s *statusStatus) Code() codes.Code {
	return s.code
}
