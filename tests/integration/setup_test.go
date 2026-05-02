package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var (
	ingestorClient ingestorv1.IngestorServiceClient
	analyzerClient analyzerv1.AnalyzerServiceClient
	alertClient    alertv1.AlertServiceClient
	geoClient      geov1.GeoIndexServiceClient
)

const (
	ingestorAddr = "localhost:50051"
	analyzerAddr = "localhost:50052"
	alertAddr    = "localhost:50053"
	geoAddr      = "localhost:50054"
	jwtSecret    = "soundmap-dev-secret-key-change-in-prod"
)

func TestMain(m *testing.M) {
	// Check if integration tests should run
	if os.Getenv("INTEGRATION_TEST") != "true" {
		fmt.Println("Skipping integration tests. Set INTEGRATION_TEST=true to run")
		os.Exit(0)
	}

	ctx := context.Background()
	
	// Create connections with auth interceptor
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(authUnaryInterceptor),
		grpc.WithStreamInterceptor(authStreamInterceptor),
	}

	// Create Ingestor connection
	ingestorConn, err := grpc.NewClient(ingestorAddr, dialOpts...)
	if err != nil {
		fmt.Printf("Failed to connect to ingestor: %v\n", err)
		os.Exit(1)
	}
	defer ingestorConn.Close()
	ingestorClient = ingestorv1.NewIngestorServiceClient(ingestorConn)

	// Create Analyzer connection
	analyzerConn, err := grpc.NewClient(analyzerAddr, dialOpts...)
	if err != nil {
		fmt.Printf("Failed to connect to analyzer: %v\n", err)
		os.Exit(1)
	}
	defer analyzerConn.Close()
	analyzerClient = analyzerv1.NewAnalyzerServiceClient(analyzerConn)

	// Create Alert connection
	alertConn, err := grpc.NewClient(alertAddr, dialOpts...)
	if err != nil {
		fmt.Printf("Failed to connect to alert engine: %v\n", err)
		os.Exit(1)
	}
	defer alertConn.Close()
	alertClient = alertv1.NewAlertServiceClient(alertConn)

	// Create Geo connection
	geoConn, err := grpc.NewClient(geoAddr, dialOpts...)
	if err != nil {
		fmt.Printf("Failed to connect to geo index: %v\n", err)
		os.Exit(1)
	}
	defer geoConn.Close()
	geoClient = geov1.NewGeoIndexServiceClient(geoConn)

	// Run tests
	code := m.Run()
	os.Exit(code)
}

func authUnaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	token := generateToken()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	return invoker(ctx, method, req, reply, cc, opts...)
}

func authStreamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	token := generateToken()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	return streamer(ctx, desc, cc, method, opts...)
}

func generateToken() string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "integration-test",
		"role": "admin",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	})
	tokenString, _ := token.SignedString([]byte(jwtSecret))
	return tokenString
}
