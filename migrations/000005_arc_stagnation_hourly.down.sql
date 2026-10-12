CREATE INDEX IF NOT EXISTS snapshots_event_id_idx ON snapshots (event_id);
DROP INDEX IF EXISTS snapshots_event_id_window_start_idx;
DROP TABLE IF EXISTS arc_stagnation_hourly;
DROP FUNCTION IF EXISTS arc_stagnation_bucket(DOUBLE PRECISION);
