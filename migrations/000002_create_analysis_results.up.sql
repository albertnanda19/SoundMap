CREATE TABLE IF NOT EXISTS analysis_results (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reading_id TEXT NOT NULL,
    sensor_id TEXT NOT NULL,
    noise_category TEXT NOT NULL DEFAULT 'NOISE_CATEGORY_UNSPECIFIED',
    health_risk_level TEXT NOT NULL DEFAULT 'HEALTH_RISK_UNSPECIFIED',
    risk_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    dominant_frequency_hz DOUBLE PRECISION,
    h3_index TEXT NOT NULL,
    h3_resolution INT NOT NULL DEFAULT 8,
    analyzed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_analysis_results_sensor_id ON analysis_results(sensor_id);
CREATE INDEX IF NOT EXISTS idx_analysis_results_h3_index ON analysis_results(h3_index);
CREATE INDEX IF NOT EXISTS idx_analysis_results_analyzed_at ON analysis_results(analyzed_at DESC);
CREATE INDEX IF NOT EXISTS idx_analysis_results_h3_analyzed ON analysis_results(h3_index, analyzed_at DESC);

SELECT create_hypertable('analysis_results', 'analyzed_at', if_not_exists => TRUE);
