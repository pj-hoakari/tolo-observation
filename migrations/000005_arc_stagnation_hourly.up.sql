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
    first_start TIMESTAMPTZ NOT NULL,
    last_end TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (event_id, hour_start, route_id, stagnation_bucket)
);

INSERT INTO arc_stagnation_hourly
SELECT s.event_id,
    date_trunc('hour', s.window_start, 'UTC'),
    route->>'route_id',
    arc_stagnation_bucket((route->>'stagnation_score')::float8),
    count(*),
    sum((route->>'stagnation_score')::float8),
    min(s.window_start),
    max(s.window_end)
FROM snapshots s
CROSS JOIN LATERAL jsonb_array_elements(s.scores->'routes') AS route
WHERE jsonb_typeof(route->'stagnation_score') = 'number'
GROUP BY 1, 2, 3, 4;

CREATE INDEX snapshots_event_id_window_start_idx ON snapshots (event_id, window_start);
DROP INDEX snapshots_event_id_idx;

ANALYZE arc_stagnation_hourly;
