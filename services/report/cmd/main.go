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
	"github.com/soundmap/soundmap/gen/go/report/v1"
	"github.com/soundmap/soundmap/services/report/internal/config"
	"github.com/soundmap/soundmap/services/report/internal/repository"
	"github.com/soundmap/soundmap/services/report/internal/service"
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

	logger.Info("starting report service",
		slog.String("version", cfg.ServiceVersion),
		slog.Int("grpc_port", cfg.GRPCPort),
		slog.Int("metrics_port", cfg.MetricsPort),
	)

	// Initialize tracer and meter
	var tracer trace.Tracer
	var meter metric.Meter
	tracer = trace.NewNoopTracerProvider().Tracer("report")
	meter = otel.Meter("report")
	_ = tracer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
	reportRepo := repository.NewPostgresReportRepository(dbPool)

	// Create report service
	reportService, err := service.NewReportService(reportRepo, logger, meter)
	if err != nil {
		logger.Error("failed to create report service", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Build gRPC server
	serverOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),
		grpc.MaxSendMsgSize(4 * 1024 * 1024),
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register services
	reportv1.RegisterReportServiceServer(grpcServer, reportService)

	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus("report", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection
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

	// Metrics HTTP server
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

			// Cancel context
			cancel()

			// Graceful shutdown with timeout
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()

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
	// Ensure analysis_results table exists (created by analyzer service)
	// Add any additional indexes for reporting
	createIndexesSQL := `
		CREATE INDEX IF NOT EXISTS idx_analysis_time_range ON analysis_results(analyzed_at);
	`

	_, err := pool.Exec(ctx, createIndexesSQL)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
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
