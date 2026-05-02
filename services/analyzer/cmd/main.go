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
	"github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/services/analyzer/internal/config"
	"github.com/soundmap/soundmap/services/analyzer/internal/consumer"
	"github.com/soundmap/soundmap/services/analyzer/internal/repository"
	"github.com/soundmap/soundmap/services/analyzer/internal/service"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
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

	logger.Info("starting analyzer service",
		slog.String("version", cfg.ServiceVersion),
		slog.Int("grpc_port", cfg.GRPCPort),
		slog.Int("metrics_port", cfg.MetricsPort),
	)

	// Initialize tracer and meter
	var tracer trace.Tracer
	var meter metric.Meter
	tracer = trace.NewNoopTracerProvider().Tracer("analyzer")
	meter = metric.NewNoopMeterProvider().Meter("analyzer")
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
	analysisRepo := &repository.PostgresAnalysisRepository{}
	// Note: In real implementation, properly initialize this
	_ = analysisRepo

	// Create NATS consumer
	natsConsumer, err := consumer.NewNATSConsumer(cfg.NATSUrl, logger)
	if err != nil {
		logger.Error("failed to create NATS consumer", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer natsConsumer.Close()

	// Create DSP analyzer and H3 util
	dspAnalyzer := &realDSPAnalyzer{}
	h3Util := &realH3Util{}

	// Create service
	svc, err := service.NewAnalyzerService(analysisRepo, dspAnalyzer, h3Util, logger, meter)
	if err != nil {
		logger.Error("failed to create service", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Build gRPC server
	serverOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),
		grpc.MaxSendMsgSize(4 * 1024 * 1024),
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register services
	analyzerv1.RegisterAnalyzerServiceServer(grpcServer, svc)

	// Register health check
	healthServer := health.NewServer()
	healthServer.SetServingStatus("analyzer", grpc_health_v1.HealthCheckResponse_SERVING)
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

	// NATS consumer goroutine
	g.Go(func() error {
		logger.Info("starting NATS consumer")
		handler := func(ctx context.Context, reading *commonv1.SensorReading) error {
			// Process the reading asynchronously
			// This is the background processing path
			logger.Debug("processing reading from NATS",
				slog.String("sensor_id", reading.SensorId),
				slog.Float64("decibel_level", reading.DecibelLevel))
			// In a real implementation, this would run the DSP pipeline
			// and save the results to the database
			return nil
		}
		if err := natsConsumer.Start(ctx, handler); err != nil {
			return fmt.Errorf("NATS consumer error: %w", err)
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

			// Cancel context to stop consumer
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
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS analysis_results (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reading_id TEXT,
			sensor_id TEXT,
			noise_category TEXT,
			health_risk_level TEXT,
			risk_score DOUBLE PRECISION,
			dominant_frequency_hz DOUBLE PRECISION,
			analyzed_at TIMESTAMPTZ DEFAULT NOW(),
			h3_index TEXT,
			h3_resolution INT
		);

		CREATE INDEX IF NOT EXISTS idx_analysis_h3 ON analysis_results(h3_index);
		CREATE INDEX IF NOT EXISTS idx_analysis_analyzed_at ON analysis_results(analyzed_at DESC);
	`

	_, err := pool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create table: %w", err)
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

// realDSPAnalyzer is a placeholder that implements service.DSPAnalyzer
// In production, this would wrap the actual pkg/dsp package
type realDSPAnalyzer struct{}

func (r *realDSPAnalyzer) ComputeFFT(samples []float64) []float64 {
	// Placeholder implementation
	return samples
}

func (r *realDSPAnalyzer) FrequencyBands(fft []float64, sampleRateHz int32) map[string]float64 {
	// Placeholder implementation
	return map[string]float64{
		"bass":     0.0,
		"low_mid":  0.0,
		"mid":      0.0,
		"high_mid": 0.0,
		"treble":   0.0,
	}
}

func (r *realDSPAnalyzer) DominantFrequency(fft []float64, sampleRateHz int32) float64 {
	// Placeholder implementation
	return 1000.0
}

func (r *realDSPAnalyzer) Analyze(decibelLevel, dominantFreq float64, bands map[string]float64) *service.AnalysisResult {
	// Placeholder implementation - categorize based on decibel level
	var category commonv1.NoiseCategory
	var riskLevel commonv1.HealthRiskLevel
	var riskScore float64

	switch {
	case decibelLevel < 40:
		category = commonv1.NoiseCategory_NOISE_CATEGORY_NATURE
		riskLevel = commonv1.HealthRiskLevel_HEALTH_RISK_SAFE
		riskScore = 10.0
	case decibelLevel < 60:
		category = commonv1.NoiseCategory_NOISE_CATEGORY_TRAFFIC
		riskLevel = commonv1.HealthRiskLevel_HEALTH_RISK_MODERATE
		riskScore = 35.0
	case decibelLevel < 80:
		category = commonv1.NoiseCategory_NOISE_CATEGORY_CONSTRUCTION
		riskLevel = commonv1.HealthRiskLevel_HEALTH_RISK_HIGH
		riskScore = 65.0
	default:
		category = commonv1.NoiseCategory_NOISE_CATEGORY_INDUSTRIAL
		riskLevel = commonv1.HealthRiskLevel_HEALTH_RISK_CRITICAL
		riskScore = 90.0
	}

	return &service.AnalysisResult{
		NoiseCategory:       category,
		HealthRiskLevel:     riskLevel,
		RiskScore:           riskScore,
		DominantFrequencyHz: dominantFreq,
		FrequencyBands:      bands,
	}
}

// realH3Util is a placeholder that implements service.H3Util
// In production, this would wrap the actual pkg/h3util package
type realH3Util struct{}

func (r *realH3Util) LatLngToH3Index(lat, lng float64, resolution int32) string {
	// Placeholder - in production, use actual H3 library
	return fmt.Sprintf("8%014x", int64((lat+90)*1000000)+int64((lng+180)*1000000)<<32)
}
