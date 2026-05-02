CREATE TABLE IF NOT EXISTS sensor_readings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sensor_id TEXT NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    decibel_level DOUBLE PRECISION NOT NULL CHECK (decibel_level >= 0 AND decibel_level <= 200),
    frequency_hz DOUBLE PRECISION,
    sensor_type TEXT NOT NULL DEFAULT 'SENSOR_TYPE_FIXED',
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sensor_readings_sensor_id ON sensor_readings(sensor_id);
CREATE INDEX IF NOT EXISTS idx_sensor_readings_recorded_at ON sensor_readings(recorded_at DESC);
CREATE INDEX IF NOT EXISTS idx_sensor_readings_decibel ON sensor_readings(decibel_level);

-- Convert to TimescaleDB hypertable
SELECT create_hypertable('sensor_readings', 'recorded_at', if_not_exists => TRUE);

-- Enable TimescaleDB compression
ALTER TABLE sensor_readings SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'sensor_id'
);
