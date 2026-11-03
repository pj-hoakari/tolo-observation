CREATE TABLE arc_flow_dwell_hourly (
    event_id TEXT NOT NULL,
    hour_start TIMESTAMPTZ NOT NULL,
    route_id TEXT NOT NULL,
    flow_bucket DOUBLE PRECISION NOT NULL,
    dwell_bucket DOUBLE PRECISION,
    sample_count BIGINT NOT NULL,
    first_start TIMESTAMPTZ NOT NULL,
    last_end TIMESTAMPTZ NOT NULL,
    UNIQUE NULLS NOT DISTINCT (event_id, hour_start, route_id, flow_bucket, dwell_bucket)
);
