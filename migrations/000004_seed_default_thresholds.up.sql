-- Seed with sensible default thresholds for demo purposes
INSERT INTO alert_thresholds (name, h3_index, max_decibel, severity, is_active) VALUES
('Global Warning Threshold', '', 75.0, 'ALERT_SEVERITY_WARNING', true),
('Global Critical Threshold', '', 90.0, 'ALERT_SEVERITY_CRITICAL', true)
ON CONFLICT DO NOTHING;
