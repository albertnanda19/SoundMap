package integration

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/soundmap/soundmap/gen/go/common/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

// generateValidToken creates a JWT token for testing
func generateValidToken(t *testing.T) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  "test-user",
		"role": "admin",
		"exp":  time.Now().Add(time.Hour).Unix(),
		"iat":  time.Now().Unix(),
	})
	tokenString, err := token.SignedString([]byte(jwtSecret))
	assert.NoError(t, err)
	return tokenString
}

// newAuthCtx returns a context with Authorization metadata set
func newAuthCtx(t *testing.T) context.Context {
	token := generateValidToken(t)
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

// makeSensorReading creates a valid SensorReading proto
func makeSensorReading(sensorID string, lat, lng, decibel, freq float64) *commonv1.SensorReading {
	return &commonv1.SensorReading{
		SensorId:     sensorID,
		Latitude:     lat,
		Longitude:    lng,
		DecibelLevel: decibel,
		FrequencyHz:  freq,
		Timestamp:    commonv1.NewTimestamp(),
		SensorType:   commonv1.SensorType_SENSOR_TYPE_FIXED,
		RawSamples:   generateRawSamples(512),
	}
}

// generateRawSamples generates sample audio data
func generateRawSamples(count int) []float64 {
	samples := make([]float64, count)
	for i := 0; i < count; i++ {
		samples[i] = math.Sin(2*math.Pi*440.0*float64(i)/44100.0) + rand.NormFloat64()*0.1
	}
	return samples
}

// waitForCondition polls until condition is true or timeout
func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	start := time.Now()
	for time.Since(start) < timeout {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

// isValidUUID checks if a string is a valid UUID
func isValidUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
