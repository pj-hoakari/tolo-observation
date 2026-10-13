ALTER TABLE arc_stagnation_hourly
    ADD COLUMN flow_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN flow_square_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN flow_stagnation_sum DOUBLE PRECISION NOT NULL DEFAULT 0;

UPDATE arc_stagnation_hourly h
SET flow_sum = f.flow_sum,
    flow_square_sum = f.flow_square_sum,
    flow_stagnation_sum = f.flow_stagnation_sum
FROM (
    SELECT s.event_id,
        date_trunc('hour', s.window_start, 'UTC') AS hour_start,
        route->>'route_id' AS route_id,
        arc_stagnation_bucket((route->>'stagnation_score')::float8) AS stagnation_bucket,
        sum(flow) AS flow_sum,
        sum(flow * flow) AS flow_square_sum,
        sum(flow * (route->>'stagnation_score')::float8) AS flow_stagnation_sum
    FROM snapshots s
    CROSS JOIN LATERAL jsonb_array_elements(s.scores->'routes') AS route
    CROSS JOIN LATERAL (
        SELECT coalesce((route->>'forward')::float8, 0) + coalesce((route->>'backward')::float8, 0) AS flow
    ) v
    WHERE jsonb_typeof(route->'stagnation_score') = 'number'
    GROUP BY 1, 2, 3, 4
) f
WHERE h.event_id = f.event_id
    AND h.hour_start = f.hour_start
    AND h.route_id = f.route_id
    AND h.stagnation_bucket = f.stagnation_bucket;

ALTER TABLE arc_stagnation_hourly
    ALTER COLUMN flow_sum DROP DEFAULT,
    ALTER COLUMN flow_square_sum DROP DEFAULT,
    ALTER COLUMN flow_stagnation_sum DROP DEFAULT;

ANALYZE arc_stagnation_hourly;
