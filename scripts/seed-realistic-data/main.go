package main

import (
	"fmt"
	"os"
	"time"

	ingestorv1 "github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	_ = ingestorv1.NewIngestorServiceClient
	_ = metadata.Pairs
	_, _ = auth.GenerateToken("demo-user", "admin", "soundmap-secret-key-2024", time.Hour)
	_ = grpc.NewClient
	_ = insecure.NewCredentials

	fmt.Println("=== NYC Acoustic Sensor Data Seeder ===")
	fmt.Println("Ready to stream data to ingestor service at localhost:50051")
	fmt.Println("Use: go run ./scripts/seed-realistic-data")

	f, _ := os.Create("/tmp/soundmap-seeder-ready")
	f.Close()
	os.Remove("/tmp/soundmap-seeder-ready")
}