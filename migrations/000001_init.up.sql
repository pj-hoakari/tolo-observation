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
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (window_end > window_start)
);

CREATE INDEX measurements_event_id_window_end_idx ON measurements (event_id, window_end);
