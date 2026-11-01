ALTER TABLE measurements ADD COLUMN edge_device_id TEXT;
CREATE UNIQUE INDEX measurements_edge_window_idx
    ON measurements (tenant_public_id, event_id, edge_device_id, observation_point_id, window_start)
    WHERE edge_device_id IS NOT NULL;
