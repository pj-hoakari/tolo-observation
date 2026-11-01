DROP INDEX IF EXISTS measurements_edge_window_idx;
ALTER TABLE measurements DROP COLUMN IF EXISTS edge_device_id;
