// Package ratelimit provides per-method rate limiting for SoundMap services.
package ratelimit

import (
	"sync"

	"golang.org/x/time/rate"
)

// MethodLimits defines rate limits for specific gRPC methods.
var MethodLimits = map[string]rate.Limit{
	// Ingestor - high throughput for sensor data
	"/ingestor.v1.IngestorService/IngestSingleReading": rate.Limit(1000), // 1000/sec
	"/ingestor.v1.IngestorService/IngestReadings":      rate.Limit(100),  // 100 streams/sec
	"/ingestor.v1.IngestorService/GetSensorStatus":     rate.Limit(100),  // 100/sec

	// Analyzer - moderate throughput
	"/analyzer.v1.AnalyzerService/AnalyzeBatch":    rate.Limit(50), // 50/sec
	"/analyzer.v1.AnalyzerService/AnalyzeStream":   rate.Limit(10), // 10 streams/sec
	"/analyzer.v1.AnalyzerService/GetZoneAnalysis": rate.Limit(10), // 10/sec

	// Alert Engine - lower throughput
	"/alert.v1.AlertEngineService/CreateThreshold": rate.Limit(10),  // 10/sec
	"/alert.v1.AlertEngineService/ListThresholds":  rate.Limit(10),  // 10/sec
	"/alert.v1.AlertEngineService/SubscribeAlerts": rate.Limit(100), // 100 subscribers/sec

	// Geo Index - moderate throughput
	"/geo.v1.GeoIndexService/QueryHotspots":     rate.Limit(50), // 50/sec
	"/geo.v1.GeoIndexService/GetHexCellStats":   rate.Limit(50), // 50/sec
	"/geo.v1.GeoIndexService/StreamCellUpdates": rate.Limit(10), // 10 streams/sec

	// Report - lower throughput for heavy queries
	"/report.v1.ReportService/GenerateZoneReport": rate.Limit(5), // 5/sec
	"/report.v1.ReportService/GenerateCityReport": rate.Limit(2), // 2/sec
}

// DefaultLimit is used for methods not explicitly configured.
const DefaultLimit = rate.Limit(50) // 50/sec

// DefaultBurst is the burst size for all limiters.
const DefaultBurst = 100

// limiterCache holds pre-created limiters for each method.
var (
	limiterCache = make(map[string]*rate.Limiter)
	cacheMu      sync.RWMutex
)

// GetLimiter returns a rate limiter for the specified gRPC method.
// This implements per-method rate limiting so high-traffic methods
// don't starve other methods.
func GetLimiter(method string) *rate.Limiter {
	// Fast path: check cache
	cacheMu.RLock()
	if lim, ok := limiterCache[method]; ok {
		cacheMu.RUnlock()
		return lim
	}
	cacheMu.RUnlock()

	// Slow path: create limiter
	cacheMu.Lock()
	defer cacheMu.Unlock()

	// Double-check after acquiring write lock
	if lim, ok := limiterCache[method]; ok {
		return lim
	}

	// Get limit for this method or use default
	lim := MethodLimits[method]
	if lim == 0 {
		lim = DefaultLimit
	}

	// Create limiter with configured burst
	limiter := rate.NewLimiter(lim, DefaultBurst)
	limiterCache[method] = limiter
	return limiter
}
