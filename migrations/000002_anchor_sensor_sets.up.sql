CREATE TABLE anchor_sensor_sets (
    event_id TEXT NOT NULL,
    point_id TEXT NOT NULL,
    route_id TEXT NOT NULL,
    observation_point_ids JSONB NOT NULL,
    changed_at TIMESTAMPTZ,
    PRIMARY KEY (event_id, point_id, route_id),
    CHECK ((point_id = '') <> (route_id = ''))
);
