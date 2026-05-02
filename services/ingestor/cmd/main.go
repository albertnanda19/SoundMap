package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/services/ingestor/internal/config"
	"github.com/soundmap/soundmap/services/ingestor/internal/publisher"
	"github.com/soundmap/soundmap/services/ingestor/internal/repository"
	"github.com/soundmap/soundmap/services/ingestor/internal/service"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	logger.Info("starting ingestor service",
		slog.String("version", cfg.ServiceVersion),
		slog.Int("grpc_port", cfg.GRPCPort),
		slog.Int("metrics_port", cfg.MetricsPort),
	)

	// Initialize tracer and meter (simplified - would connect to OTLP in production)
	var tracer trace.Tracer
	var meter metric.Meter
	tracer = trace.NewNoopTracerProvider().Tracer("ingestor")
	meter = otel.Meter("ingestor")
	_ = tracer

	ctx := context.Background()

	// Create database pool
	dbPool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create database pool", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer dbPool.Close()

	// Run migrations
	if err := runMigrations(ctx, dbPool); err != nil {
		logger.Error("failed to run migrations", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create repository
	readingRepo, err := repository.NewPostgresReadingRepository(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create reading repository", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer readingRepo.Close()

	// Create NATS publisher
	natsPub, err := publisher.NewNATSPublisher(cfg.NATSUrl)
	if err != nil {
		logger.Error("failed to create NATS publisher", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer natsPub.Close()

	// Create service
	svc, err := service.NewIngestorService(readingRepo, natsPub, logger, meter)
	if err != nil {
		logger.Error("failed to create service", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Build gRPC server options
	serverOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),  // 4MB
		grpc.MaxSendMsgSize(4 * 1024 * 1024),  // 4MB
	}

	// Create gRPC server
	grpcServer := grpc.NewServer(serverOptions...)
	
	// Register services
	ingestorv1.RegisterIngestorServiceServer(grpcServer, svc)
	
	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus("ingestor", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	
	// Register reflection for debugging
	reflection.Register(grpcServer)

	// Start gRPC server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		logger.Error("failed to listen", slog.String("error", err.Error()))
		os.Exit(1)
	}

	g, ctx := errgroup.WithContext(ctx)

	// gRPC server goroutine
	g.Go(func() error {
		logger.Info("starting gRPC server", slog.Int("port", cfg.GRPCPort))
		if err := grpcServer.Serve(lis); err != nil {
			return fmt.Errorf("gRPC server error: %w", err)
		}
		return nil
	})

	// Metrics HTTP server goroutine
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	})
	metricsMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ready"}`))
	})

	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: metricsMux,
	}

	g.Go(func() error {
		logger.Info("starting metrics server", slog.Int("port", cfg.MetricsPort))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("metrics server error: %w", err)
		}
		return nil
	})

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	g.Go(func() error {
		select {
		case sig := <-sigChan:
			logger.Info("received shutdown signal", slog.String("signal", sig.String()))
			
			// Graceful shutdown with timeout
			shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			
			// Stop gRPC server
			grpcServer.GracefulStop()
			
			// Shutdown metrics server
			if err := metricsServer.Shutdown(shutdownCtx); err != nil {
				logger.Error("failed to shutdown metrics server", slog.String("error", err.Error()))
			}
			
			logger.Info("shutdown complete")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	if err := g.Wait(); err != nil {
		logger.Error("server error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Create table if not exists
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS sensor_readings (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			sensor_id TEXT NOT NULL,
			latitude DOUBLE PRECISION,
			longitude DOUBLE PRECISION,
			decibel_level DOUBLE PRECISION,
			frequency_hz DOUBLE PRECISION,
			sensor_type TEXT,
			recorded_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		
		SELECT create_hypertable('sensor_readings', 'recorded_at', if_not_exists => TRUE);
	`
	
	_, err := pool.Exec(ctx, createTableSQL)
	if err != nil {
		// Hypertable might already exist, try without it
		simpleCreate := `
			CREATE TABLE IF NOT EXISTS sensor_readings (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				sensor_id TEXT NOT NULL,
				latitude DOUBLE PRECISION,
				longitude DOUBLE PRECISION,
				decibel_level DOUBLE PRECISION,
				frequency_hz DOUBLE PRECISION,
				sensor_type TEXT,
				recorded_at TIMESTAMPTZ NOT NULL,
				created_at TIMESTAMPTZ DEFAULT NOW()
			);
		`
		_, err = pool.Exec(ctx, simpleCreate)
		if err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
	}
	
	return nil
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
