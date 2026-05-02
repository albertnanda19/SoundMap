package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/soundmap/soundmap/cmd/gateway/internal/config"
	"github.com/soundmap/soundmap/cmd/gateway/internal/proxy"
	"github.com/soundmap/soundmap/gen/go/alert/v1"
	"github.com/soundmap/soundmap/gen/go/analyzer/v1"
	"github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/soundmap/soundmap/gen/go/ingestor/v1"
	"github.com/soundmap/soundmap/gen/go/report/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(os.Stdout, os.Stderr, os.Stderr, 0))

	logger.Info("starting API gateway",
		slog.String("service", cfg.ServiceName),
		slog.Int("http_port", cfg.HTTPPort),
		slog.Int("grpc_port", cfg.GRPCPort),
	)

	// Create service clients
	clients, closeFuncs, err := proxy.NewServiceClients(cfg)
	if err != nil {
		logger.Error("failed to create service clients", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if err := proxy.Close(closeFuncs); err != nil {
			logger.Error("failed to close connections", slog.String("error", err.Error()))
		}
	}()

	// Create gRPC-gateway mux
	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(customHeaderMatcher),
		runtime.WithErrorHandler(customErrorHandler),
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions:   runtime.JSONPbMarshalerOptions{Indent: "  "},
			UnmarshalOptions: runtime.JSONPbUnmarshalerOptions{DiscardUnknown: true},
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register service handlers
	logger.Info("registering service handlers")

	if err := ingestorv1.RegisterIngestorServiceHandlerClient(ctx, mux, clients.Ingestor); err != nil {
		logger.Error("failed to register ingestor handler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := analyzerv1.RegisterAnalyzerServiceHandlerClient(ctx, mux, clients.Analyzer); err != nil {
		logger.Error("failed to register analyzer handler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := alertv1.RegisterAlertServiceHandlerClient(ctx, mux, clients.Alert); err != nil {
		logger.Error("failed to register alert handler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := geov1.RegisterGeoIndexServiceHandlerClient(ctx, mux, clients.Geo); err != nil {
		logger.Error("failed to register geo handler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := reportv1.RegisterReportServiceHandlerClient(ctx, mux, clients.Report); err != nil {
		logger.Error("failed to register report handler", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Create HTTP server with CORS
	httpMux := http.NewServeMux()
	httpMux.Handle("/v1/", corsMiddleware(mux))
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "ok"}`))
	})
	httpMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("# Metrics endpoint placeholder\n"))
	})

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: httpMux,
	}

	// Start HTTP server
	go func() {
		logger.Info("HTTP server listening", slog.Int("port", cfg.HTTPPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", slog.String("error", err.Error()))
		}
	}()

	// Log registered endpoints
	logger.Info("registered REST endpoints",
		slog.String("POST /v1/ingest/single", "IngestSingleReading"),
		slog.String("GET /v1/sensors/{sensor_id}/status", "GetSensorStatus"),
		slog.String("POST /v1/analyze/batch", "AnalyzeBatch"),
		slog.String("GET /v1/geo/hotspots", "QueryHotspots"),
		slog.String("GET /v1/geo/cells/{h3_index}", "GetHexCellStats"),
		slog.String("POST /v1/alerts/thresholds", "CreateThreshold"),
		slog.String("GET /v1/alerts/thresholds", "ListThresholds"),
		slog.String("POST /v1/alerts/{alert_id}/acknowledge", "AcknowledgeAlert"),
		slog.String("GET /v1/reports/zone/{h3_index}", "GenerateZoneReport"),
		slog.String("GET /v1/reports/city/{city_name}", "GenerateCityReport"),
	)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	<-sigChan
	logger.Info("received shutdown signal, gracefully shutting down")

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown HTTP server", slog.String("error", err.Error()))
	}

	logger.Info("shutdown complete")
}

// customHeaderMatcher allows the Authorization header to be forwarded
func customHeaderMatcher(key string) (string, bool) {
	switch key {
	case "Authorization":
		return "authorization", true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}

// customErrorHandler maps gRPC errors to proper HTTP status codes
func customErrorHandler(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	st, ok := status.FromError(err)
	if !ok {
		// Unknown error
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	httpStatus := grpcCodeToHTTPStatus(st.Code())
	w.WriteHeader(httpStatus)
	w.Header().Set("Content-Type", "application/json")

	// Write error response
	msg := st.Message()
	if msg == "" {
		msg = "unknown error"
	}
	w.Write([]byte(fmt.Sprintf(`{"code": %d, "message": "%s", "details": "%s"}`,
		httpStatus, http.StatusText(httpStatus), msg)))
}

// grpcCodeToHTTPStatus maps gRPC status codes to HTTP status codes
func grpcCodeToHTTPStatus(code codes.Code) int {
	switch code {
	case codes.OK:
		return http.StatusOK
	case codes.Canceled:
		return http.StatusRequestTimeout
	case codes.Unknown:
		return http.StatusInternalServerError
	case codes.InvalidArgument:
		return http.StatusBadRequest
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists:
		return http.StatusConflict
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.FailedPrecondition:
		return http.StatusPreconditionFailed
	case codes.Aborted:
		return http.StatusConflict
	case codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unimplemented:
		return http.StatusNotImplemented
	case codes.Internal:
		return http.StatusInternalServerError
	case codes.Unavailable:
		return http.StatusServiceUnavailable
	case codes.DataLoss:
		return http.StatusInternalServerError
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

// corsMiddleware adds CORS headers to responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
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
