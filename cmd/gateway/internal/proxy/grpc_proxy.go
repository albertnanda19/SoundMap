package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/gen/go/report/v1"
	"github.com/soundmap/soundmap/cmd/gateway/internal/config"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ServiceClients holds gRPC clients for all downstream services
type ServiceClients struct {
	Ingestor ingestorv1.IngestorServiceClient
	Analyzer analyzerv1.AnalyzerServiceClient
	Alert    alertv1.AlertServiceClient
	Geo      geov1.GeoIndexServiceClient
	Report   reportv1.ReportServiceClient
}

// NewServiceClients creates gRPC connections and clients for all services
func NewServiceClients(cfg *config.Config) (*ServiceClients, []func() error, error) {
	var closeFuncs []func() error

	// Determine transport credentials
	var creds credentials.TransportCredentials
	if cfg.TLSEnabled {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true, // For dev mode - in production, verify server cert
		}
		creds = credentials.NewTLS(tlsConfig)
	} else {
		creds = insecure.NewCredentials()
	}

	// Common dial options
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	}

	// Helper function to create a connection
	createConn := func(name, addr string) (*grpc.ClientConn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s connection: %w", name, err)
		}
		
		// Test connection with a simple context check
		_, err = conn.WaitForStateChange(ctx, grpc.Connecting)
		if err != nil {
			// Continue anyway - gRPC handles reconnection
		}
		
		return conn, nil
	}

	clients := &ServiceClients{}

	// Create Ingestor connection
	ingestorConn, err := createConn("ingestor", cfg.IngestorAddr)
	if err != nil {
		return nil, closeFuncs, err
	}
	clients.Ingestor = ingestorv1.NewIngestorServiceClient(ingestorConn)
	closeFuncs = append(closeFuncs, func() error { return ingestorConn.Close() })

	// Create Analyzer connection
	analyzerConn, err := createConn("analyzer", cfg.AnalyzerAddr)
	if err != nil {
		return nil, closeFuncs, err
	}
	clients.Analyzer = analyzerv1.NewAnalyzerServiceClient(analyzerConn)
	closeFuncs = append(closeFuncs, func() error { return analyzerConn.Close() })

	// Create Alert connection
	alertConn, err := createConn("alert", cfg.AlertAddr)
	if err != nil {
		return nil, closeFuncs, err
	}
	clients.Alert = alertv1.NewAlertServiceClient(alertConn)
	closeFuncs = append(closeFuncs, func() error { return alertConn.Close() })

	// Create Geo connection
	geoConn, err := createConn("geo", cfg.GeoAddr)
	if err != nil {
		return nil, closeFuncs, err
	}
	clients.Geo = geov1.NewGeoIndexServiceClient(geoConn)
	closeFuncs = append(closeFuncs, func() error { return geoConn.Close() })

	// Create Report connection
	reportConn, err := createConn("report", cfg.ReportAddr)
	if err != nil {
		return nil, closeFuncs, err
	}
	clients.Report = reportv1.NewReportServiceClient(reportConn)
	closeFuncs = append(closeFuncs, func() error { return reportConn.Close() })

	return clients, closeFuncs, nil
}

// Close closes all connections and joins any errors
func Close(closeFuncs []func() error) error {
	var errs []error
	for _, fn := range closeFuncs {
		if err := fn(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
