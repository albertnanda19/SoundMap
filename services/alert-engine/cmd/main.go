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
	"github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/soundmap/soundmap/services/alert-engine/internal/config"
	"github.com/soundmap/soundmap/services/alert-engine/internal/consumer"
	"github.com/soundmap/soundmap/services/alert-engine/internal/engine"
	"github.com/soundmap/soundmap/services/alert-engine/internal/repository"
	"github.com/soundmap/soundmap/services/alert-engine/internal/service"
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

	logger.Info("starting alert-engine service",
		slog.String("version", cfg.ServiceVersion),
		slog.Int("grpc_port", cfg.GRPCPort),
		slog.Int("metrics_port", cfg.MetricsPort),
	)

	// Initialize tracer and meter
	var tracer trace.Tracer
	var meter metric.Meter
	tracer = trace.NewNoopTracerProvider().Tracer("alert-engine")
	meter = otel.Meter("alert-engine")
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

	// Create repositories
	thresholdRepo, err := repository.NewPostgresThresholdRepository(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create threshold repository", slog.String("error", err.Error()))
		os.Exit(1)
	}

	alertRepo := repository.NewPostgresAlertRepository(dbPool)

	// Create alert engine
	alertEngine, err := engine.NewAlertEngine(thresholdRepo, alertRepo, logger, meter)
	if err != nil {
		logger.Error("failed to create alert engine", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create H3 utility (placeholder - use real implementation in production)
	h3Util := &realH3Util{}

	// Create analysis consumer
	analysisConsumer, err := consumer.NewAnalysisConsumer(cfg.NATSUrl, logger, alertEngine, h3Util)
	if err != nil {
		logger.Error("failed to create analysis consumer", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer analysisConsumer.Close()

	// Create alert service
	alertService, err := service.NewAlertService(alertEngine, thresholdRepo, alertRepo, logger, meter)
	if err != nil {
		logger.Error("failed to create alert service", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Build gRPC server
	serverOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),
		grpc.MaxSendMsgSize(4 * 1024 * 1024),
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register services
	alertv1.RegisterAlertServiceServer(grpcServer, alertService)

	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus("alert-engine", grpc_health_v1.HealthCheckResponse_SERVING)
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

	// Alert engine goroutine
	g.Go(func() error {
		logger.Info("starting alert engine")
		if err := alertEngine.Start(ctx); err != nil {
			return fmt.Errorf("alert engine error: %w", err)
		}
		return nil
	})

	// Analysis consumer goroutine
	g.Go(func() error {
		logger.Info("starting analysis consumer")
		if err := analysisConsumer.Start(ctx); err != nil {
			return fmt.Errorf("analysis consumer error: %w", err)
		}
		return nil
	})

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

			// Cancel context to stop all components
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
	createTablesSQL := `
		CREATE TABLE IF NOT EXISTS alert_thresholds (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			h3_index TEXT,
			max_decibel DOUBLE PRECISION NOT NULL,
			severity TEXT NOT NULL,
			is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS fired_alerts (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			threshold_id UUID REFERENCES alert_thresholds(id),
			sensor_id TEXT,
			h3_index TEXT,
			severity TEXT,
			current_decibel DOUBLE PRECISION,
			threshold_decibel DOUBLE PRECISION,
			message TEXT,
			fired_at TIMESTAMPTZ DEFAULT NOW(),
			acknowledged_at TIMESTAMPTZ,
			acknowledged_by TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_alert_thresholds_active ON alert_thresholds(is_active);
		CREATE INDEX IF NOT EXISTS idx_fired_alerts_unack ON fired_alerts(acknowledged_at) WHERE acknowledged_at IS NULL;
	`

	_, err := pool.Exec(ctx, createTablesSQL)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
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

// realH3Util is a placeholder for H3 geospatial utility
type realH3Util struct{}

func (r *realH3Util) LatLngToH3Index(lat, lng float64, resolution int32) string {
	// Placeholder - in production use actual H3 library
	return fmt.Sprintf("8%014x", int64((lat+90)*1000000)+int64((lng+180)*1000000)<<32)
}
