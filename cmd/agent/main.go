package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/soundmap/soundmap/cmd/agent/internal/client"
	"github.com/soundmap/soundmap/cmd/agent/internal/config"
	"github.com/soundmap/soundmap/cmd/agent/internal/simulator"
	"golang.org/x/sync/errgroup"
)

func main() {
	// Print startup banner
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    SoundMap Sensor Agent                      ║")
	fmt.Println("║              IoT Sensor Simulator & Data Streamer            ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Load config
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.LogLevel),
	}))

	// Generate JWT token if not provided
	jwtToken := cfg.JWTToken
	if jwtToken == "" {
		var err error
		jwtToken, err = generateJWTToken("agent-01", "sensor", cfg.JWTSecretKey, 24*time.Hour)
		if err != nil {
			logger.Error("Failed to generate JWT token", slog.String("error", err.Error()))
			os.Exit(1)
		}
		logger.Info("Generated JWT token", slog.String("token", jwtToken[:50]+"..."))
	}

	// Create sensor fleet
	sensors := simulator.NewSensorFleet(cfg.SensorCount, cfg.CityCenter.Lat, cfg.CityCenter.Lng, cfg.RadiusKm, cfg.ScenarioName)

	// Print sensor table
	fmt.Printf("\n%-40s %-10s %-10s %-15s %-10s\n", "Sensor ID", "Lat", "Lng", "Type", "Scenario")
	fmt.Println(string(make([]byte, 100)))
	printLimit := 10
	if len(sensors) < printLimit {
		printLimit = len(sensors)
	}
	for i := 0; i < printLimit; i++ {
		s := sensors[i]
		fmt.Printf("%-40s %-10.4f %-10.4f %-15s %-10s\n",
			s.SensorID, s.Lat, s.Lng, s.SensorType.String(), s.Scenario)
	}
	if len(sensors) > printLimit {
		fmt.Printf("... and %d more sensors\n", len(sensors)-printLimit)
	}

	fmt.Printf("\nTarget: %s\n", cfg.IngestorAddr)
	fmt.Printf("Sensors: %d | Interval: %dms | Scenario: %s\n",
		cfg.SensorCount, cfg.SendIntervalMs, cfg.ScenarioName)

	if cfg.DurationSeconds > 0 {
		fmt.Printf("Duration: %d seconds\n", cfg.DurationSeconds)
	} else {
		fmt.Println("Duration: unlimited (press Ctrl+C to stop)")
	}
	fmt.Println()

	// Create ingestor client
	ingestorClient, err := client.NewGRPCIngestorClient(cfg.IngestorAddr, jwtToken, cfg.TLSEnabled, cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSCAFile)
	if err != nil {
		logger.Error("Failed to create ingestor client", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer ingestorClient.Close()

	// Setup context
	var ctx context.Context
	var cancel context.CancelFunc
	if cfg.DurationSeconds > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(cfg.DurationSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	g, ctx := errgroup.WithContext(ctx)

	// Stats tracking
	stats := &Stats{}
	statsTicker := time.NewTicker(5 * time.Second)
	defer statsTicker.Stop()

	// Stats reporter goroutine
	g.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-statsTicker.C:
				elapsed := time.Since(stats.startTime).Seconds()
				if elapsed > 0 {
					rate := float64(stats.sentCount) / elapsed
					logger.Info("Live stats",
						slog.Int("sent", stats.sentCount),
						slog.Float64("rate_per_sec", rate),
						slog.String("duration", time.Since(stats.startTime).String()))
				}
			}
		}
	})

	// Main streaming goroutine
	g.Go(func() error {
		err := ingestorClient.StreamReadings(ctx, sensors, cfg.SendIntervalMs)
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			return err
		}
		return nil
	})

	// Wait for signal or completion
	select {
	case sig := <-sigChan:
		logger.Info("Received signal, shutting down", slog.String("signal", sig.String()))
		cancel()
	case <-ctx.Done():
		logger.Info("Duration completed")
	}

	// Wait for goroutines to finish
	if err := g.Wait(); err != nil {
		logger.Error("Error during execution", slog.String("error", err.Error()))
	}

	// Print final stats
	elapsed := time.Since(stats.startTime).Seconds()
	logger.Info("Final stats",
		slog.Int("total_sent", stats.sentCount),
		slog.Float64("total_seconds", elapsed),
		slog.Float64("avg_rate", float64(stats.sentCount)/elapsed))

	fmt.Println("\nGoodbye!")
}

// Stats tracks runtime statistics
type Stats struct {
	sentCount int
	startTime time.Time
}

func init() {
	// Seed random number generator
	// (Not needed in Go 1.20+ as rand is automatically seeded)
}

// generateJWTToken creates a JWT token for the agent
func generateJWTToken(subject, role, secretKey string, expiry time.Duration) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  subject,
		"role": role,
		"exp":  time.Now().Add(expiry).Unix(),
		"iat":  time.Now().Unix(),
	})

	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
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
