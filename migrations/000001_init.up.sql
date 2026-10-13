CREATE TABLE edge_devices (
    id BIGSERIAL PRIMARY KEY,
    edge_device_id TEXT NOT NULL UNIQUE,
    tenant_public_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    name TEXT NOT NULL,
    unregistered BOOLEAN NOT NULL DEFAULT false,
    last_heartbeat_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX edge_devices_event_id_idx ON edge_devices (event_id);

CREATE TABLE observation_points (
    id BIGSERIAL PRIMARY KEY,
    observation_point_id TEXT NOT NULL UNIQUE,
    edge_device_id BIGINT NOT NULL REFERENCES edge_devices (id),
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    last_active_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX observation_points_edge_device_id_idx ON observation_points (edge_device_id);

CREATE TABLE measurements (
    id BIGSERIAL PRIMARY KEY,
    tenant_public_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    observation_point_id TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    count_in INTEGER NOT NULL,
    count_out INTEGER NOT NULL,
    rate_in DOUBLE PRECISION NOT NULL,
    rate_out DOUBLE PRECISION NOT NULL,
    mean_detected_people DOUBLE PRECISION CHECK (mean_detected_people >= 0),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (window_end > window_start)
);
CREATE INDEX measurements_event_id_window_end_idx ON measurements (event_id, window_end);

CREATE TABLE snapshots (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id TEXT NOT NULL UNIQUE,
    tenant_public_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    scores JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX snapshots_event_id_window_start_idx ON snapshots (event_id, window_start);

CREATE TABLE optimization_results (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    verdict TEXT NOT NULL,
    detection_state JSONB NOT NULL,
    optimization_result JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX optimization_results_event_id_idx ON optimization_results (event_id);

CREATE TABLE observation_settings (
    event_id TEXT PRIMARY KEY,
    tenant_public_id TEXT NOT NULL,
    history_window_days INTEGER NOT NULL CHECK (history_window_days >= 1),
    event_type TEXT NOT NULL CHECK (event_type IN ('short_term', 'long_term')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE FUNCTION arc_stagnation_bucket(stagnation DOUBLE PRECISION) RETURNS DOUBLE PRECISION
    LANGUAGE sql IMMUTABLE STRICT
    RETURN CASE WHEN stagnation <= 0 THEN 0 ELSE 2 * 1.02 ^ ceil(ln(stagnation) / ln(1.02)) / 2.02 END;

CREATE TABLE arc_stagnation_hourly (
    event_id TEXT NOT NULL,
    hour_start TIMESTAMPTZ NOT NULL,
    route_id TEXT NOT NULL,
    stagnation_bucket DOUBLE PRECISION NOT NULL,
    sample_count BIGINT NOT NULL,
    stagnation_sum DOUBLE PRECISION NOT NULL,
    flow_sum DOUBLE PRECISION NOT NULL,
    flow_square_sum DOUBLE PRECISION NOT NULL,
    flow_stagnation_sum DOUBLE PRECISION NOT NULL,
    first_start TIMESTAMPTZ NOT NULL,
    last_end TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (event_id, hour_start, route_id, stagnation_bucket)
);
