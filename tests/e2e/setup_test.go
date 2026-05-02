//go:build e2e
// +build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	alertv1 "github.com/soundmap/soundmap/gen/go/alert/v1"
	analyzerv1 "github.com/soundmap/soundmap/gen/go/analyzer/v1"
	geov1 "github.com/soundmap/soundmap/gen/go/geo/v1"
	ingestorv1 "github.com/soundmap/soundmap/gen/go/ingestor/v1"
	reportv1 "github.com/soundmap/soundmap/gen/go/report/v1"
)

// E2ESuite holds all gRPC clients and shared test state
type E2ESuite struct {
	IngestorConn *grpc.ClientConn
	AnalyzerConn *grpc.ClientConn
	AlertConn    *grpc.ClientConn
	GeoConn      *grpc.ClientConn
	ReportConn   *grpc.ClientConn

	Ingestor ingestorv1.IngestorServiceClient
	Analyzer analyzerv1.AnalyzerServiceClient
	Alert    alertv1.AlertServiceClient
	Geo      geov1.GeoIndexServiceClient
	Report   reportv1.ReportServiceClient

	AuthToken  string
	GatewayURL string
	HTTPClient *http.Client
}

var suite *E2ESuite

func TestMain(m *testing.M) {
	s, err := setupSuite()
	if err != nil {
		fmt.Fprintf(os.Stderr, "E2E setup failed: %v\n", err)
		os.Exit(1)
	}
	suite = s
	code := m.Run()
	teardownSuite(s)
	os.Exit(code)
}

func setupSuite() (*E2ESuite, error) {
	// Read config from environment with sensible defaults
	ingestorAddr := getEnvOrDefault("INGESTOR_ADDR", "localhost:50051")
	analyzerAddr := getEnvOrDefault("ANALYZER_ADDR", "localhost:50052")
	alertAddr := getEnvOrDefault("ALERT_ADDR", "localhost:50053")
	geoAddr := getEnvOrDefault("GEO_ADDR", "localhost:50054")
	reportAddr := getEnvOrDefault("REPORT_ADDR", "localhost:50055")
	gatewayURL := getEnvOrDefault("GATEWAY_URL", "http://localhost:8080")
	jwtSecretKey := getEnvOrDefault("JWT_SECRET_KEY", "soundmap-dev-secret-key-change-in-prod")

	s := &E2ESuite{
		GatewayURL: gatewayURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}

	// Generate auth token for test user
	token, err := generateTestToken("e2e-test-user", "admin", jwtSecretKey)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	s.AuthToken = token

	// Create gRPC connections
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conns := []struct {
		addr string
		conn **grpc.ClientConn
	}{
		{ingestorAddr, &s.IngestorConn},
		{analyzerAddr, &s.AnalyzerConn},
		{alertAddr, &s.AlertConn},
		{geoAddr, &s.GeoConn},
		{reportAddr, &s.ReportConn},
	}
	for _, c := range conns {
		conn, err := grpc.DialContext(dialCtx, c.addr, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", c.addr, err)
		}
		*c.conn = conn
	}

	// Create service clients
	s.Ingestor = ingestorv1.NewIngestorServiceClient(s.IngestorConn)
	s.Analyzer = analyzerv1.NewAnalyzerServiceClient(s.AnalyzerConn)
	s.Alert = alertv1.NewAlertServiceClient(s.AlertConn)
	s.Geo = geov1.NewGeoIndexServiceClient(s.GeoConn)
	s.Report = reportv1.NewReportServiceClient(s.ReportConn)

	return s, nil
}

func teardownSuite(s *E2ESuite) {
	s.IngestorConn.Close()
	s.AnalyzerConn.Close()
	s.AlertConn.Close()
	s.GeoConn.Close()
	s.ReportConn.Close()
}

// authCtx returns a context with the Bearer token in gRPC metadata
func authCtx(t *testing.T) context.Context {
	t.Helper()
	md := metadata.Pairs("authorization", "Bearer "+suite.AuthToken)
	return metadata.NewOutgoingContext(context.Background(), md)
}

// ctxTimeout returns authCtx with a deadline
func ctxTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(authCtx(t), d)
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// generateTestToken creates a JWT token for testing
func generateTestToken(subject, role, secret string) (string, error) {
	// Simple JWT token generation for testing
	// In production, use proper JWT library
	header := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	payload := fmt.Sprintf("eyJzdWIiOiIlcyIsInJvbGUiOiIlcyIsImV4cCI6MTgyNTAwMDAwMH0", subject, role)
	signature := "test-signature"
	return fmt.Sprintf("%s.%s.%s", header, payload, signature), nil
}
