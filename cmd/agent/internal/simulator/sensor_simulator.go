package simulator

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/soundmap/soundmap/gen/go/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Sensor represents a simulated IoT sensor device
type Sensor struct {
	SensorID    string
	Lat         float64
	Lng         float64
	SensorType  commonv1.SensorType
	BaseDecibel float64
	Scenario    string
	resolution  int32 // H3 resolution
}

// NewSensorFleet generates a fleet of sensors based on the scenario
func NewSensorFleet(count int, centerLat, centerLng, radiusKm float64, scenario string) []*Sensor {
	sensors := make([]*Sensor, count)

	for i := 0; i < count; i++ {
		// Random position within radius
		distance := radiusKm * 1000 * math.Sqrt(rand.Float64()) // meters, sqrt for uniform distribution
		angle := rand.Float64() * 2 * math.Pi

		// Convert to lat/lng offset (approximate)
		latOffset := distance * math.Cos(angle) / 111320.0 // meters to degrees
		lngOffset := distance * math.Sin(angle) / (111320.0 * math.Cos(centerLat*math.Pi/180))

		// Determine sensor type
		sensorType := determineSensorType()

		// Determine base decibel based on scenario
		baseDecibel := determineBaseDecibel(scenario)

		sensors[i] = &Sensor{
			SensorID:    uuid.New().String(),
			Lat:         centerLat + latOffset,
			Lng:         centerLng + lngOffset,
			SensorType:  sensorType,
			BaseDecibel: baseDecibel,
			Scenario:    scenario,
			resolution:  9,
		}
	}

	return sensors
}

func determineSensorType() commonv1.SensorType {
	r := rand.Float64()
	switch {
	case r < 0.70:
		return commonv1.SensorType_SENSOR_TYPE_FIXED
	case r < 0.90:
		return commonv1.SensorType_SENSOR_TYPE_MOBILE
	default:
		return commonv1.SensorType_SENSOR_TYPE_VIRTUAL
	}
}

func determineBaseDecibel(scenario string) float64 {
	switch scenario {
	case "traffic":
		return 65 + rand.Float64()*15 // 65-80 dB
	case "construction":
		return 80 + rand.Float64()*15 // 80-95 dB
	case "quiet":
		return 35 + rand.Float64()*15 // 35-50 dB
	case "mixed":
		return 40 + rand.Float64()*40 // 40-80 dB random
	default:
		return 50 + rand.Float64()*20 // 50-70 dB default
	}
}

// GenerateReading creates a sensor reading with realistic noise patterns
func (s *Sensor) GenerateReading(t time.Time) *commonv1.SensorReading {
	if !s.IsActiveNow(t) {
		return nil
	}

	decibel := s.calculateDecibel(t)
	frequency := s.calculateFrequency()
	rawSamples := s.generateRawSamples(frequency)

	return &commonv1.SensorReading{
		SensorId:     s.SensorID,
		Latitude:     s.Lat,
		Longitude:    s.Lng,
		DecibelLevel: decibel,
		FrequencyHz:  frequency,
		Timestamp:    timestamppb.New(t),
		SensorType:   s.SensorType,
		RawSamples:   rawSamples,
	}
}

func (s *Sensor) calculateDecibel(t time.Time) float64 {
	decibel := s.BaseDecibel

	// Add gaussian noise (mean=0, stddev=3)
	noise := rand.NormFloat64() * 3
	decibel += noise

	hour := t.Hour()

	switch s.Scenario {
	case "traffic":
		// Rush hour spikes: 7-9 AM and 5-7 PM
		if (hour >= 7 && hour <= 9) || (hour >= 17 && hour <= 19) {
			decibel *= 1.2
		}
	case "construction":
		// Active only during work hours
		if hour < 8 || hour > 18 {
			decibel *= 0.3 // Significant drop after hours
		}
	case "quiet":
		// Low variance, slight day/night difference
		if hour >= 22 || hour <= 6 {
			decibel *= 0.8 // Quieter at night
		}
	}

	// Clamp to realistic range
	if decibel < 20 {
		decibel = 20
	}
	if decibel > 130 {
		decibel = 130
	}

	return decibel
}

func (s *Sensor) calculateFrequency() float64 {
	switch s.Scenario {
	case "traffic":
		return 80 + rand.Float64()*420 // 80-500 Hz (engine rumble, tires)
	case "construction":
		return 200 + rand.Float64()*800 // 200-1000 Hz (machinery, tools)
	case "quiet":
		return 2000 + rand.Float64()*6000 // 2000-8000 Hz (birds, wind, leaves)
	case "mixed":
		return 100 + rand.Float64()*2900 // 100-3000 Hz random
	default:
		return 500 + rand.Float64()*1000
	}
}

func (s *Sensor) generateRawSamples(dominantFreq float64) []float64 {
	const (
		sampleRate = 44100
		numSamples = 512
	)

	samples := make([]float64, numSamples)
	
	// Generate a complex waveform with fundamental + harmonics + noise
	for i := 0; i < numSamples; i++ {
		t := float64(i) / sampleRate
		
		// Fundamental frequency
		sample := 0.5 * math.Sin(2*math.Pi*dominantFreq*t)
		
		// Add harmonics
		sample += 0.25 * math.Sin(2*math.Pi*dominantFreq*2*t) // 2nd harmonic
		sample += 0.125 * math.Sin(2*math.Pi*dominantFreq*3*t) // 3rd harmonic
		
		// Add white noise
		sample += rand.NormFloat64() * 0.1
		
		// Clamp to [-1, 1]
		if sample > 1 {
			sample = 1
		}
		if sample < -1 {
			sample = -1
		}
		
		samples[i] = sample
	}
	
	return samples
}

// IsActiveNow checks if the sensor is active at the given time
func (s *Sensor) IsActiveNow(t time.Time) bool {
	if s.Scenario != "construction" {
		return true // Always active for non-construction
	}

	hour := t.Hour()
	// Construction: 08:00 - 18:00
	return hour >= 8 && hour <= 18
}

// String returns a string representation of the sensor for logging
func (s *Sensor) String() string {
	return fmt.Sprintf("Sensor{ID=%s, Lat=%.4f, Lng=%.4f, Type=%s, Scenario=%s, BaseDb=%.1f}",
		s.SensorID[:8], s.Lat, s.Lng, s.SensorType.String(), s.Scenario, s.BaseDecibel)
}
