CREATE TABLE IF NOT EXISTS alert_thresholds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    h3_index TEXT NOT NULL DEFAULT '',
    max_decibel DOUBLE PRECISION NOT NULL CHECK (max_decibel > 0),
    severity TEXT NOT NULL DEFAULT 'ALERT_SEVERITY_INFO',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS fired_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    threshold_id UUID NOT NULL REFERENCES alert_thresholds(id) ON DELETE CASCADE,
    sensor_id TEXT NOT NULL,
    h3_index TEXT NOT NULL,
    severity TEXT NOT NULL,
    current_decibel DOUBLE PRECISION NOT NULL,
    threshold_decibel DOUBLE PRECISION NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    fired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by TEXT
);

CREATE INDEX IF NOT EXISTS idx_fired_alerts_threshold_id ON fired_alerts(threshold_id);
CREATE INDEX IF NOT EXISTS idx_fired_alerts_fired_at ON fired_alerts(fired_at DESC);
CREATE INDEX IF NOT EXISTS idx_fired_alerts_acknowledged ON fired_alerts(acknowledged_at) WHERE acknowledged_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_alert_thresholds_h3_active ON alert_thresholds(h3_index, is_active) WHERE is_active = true;
