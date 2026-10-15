CREATE TEMPORARY TABLE arc_stagnation_hourly_merged AS
SELECT event_id, hour_start, route_id, stagnation_bucket,
    sum(sample_count) AS sample_count,
    sum(stagnation_sum) AS stagnation_sum,
    sum(flow_sum) AS flow_sum,
    sum(flow_square_sum) AS flow_square_sum,
    sum(flow_stagnation_sum) AS flow_stagnation_sum,
    min(first_start) AS first_start,
    max(last_end) AS last_end
FROM arc_stagnation_hourly
GROUP BY event_id, hour_start, route_id, stagnation_bucket;

DELETE FROM arc_stagnation_hourly;

ALTER TABLE arc_stagnation_hourly DROP CONSTRAINT arc_stagnation_hourly_pkey;

ALTER TABLE arc_stagnation_hourly DROP COLUMN same_sensor_flow;

ALTER TABLE arc_stagnation_hourly ADD PRIMARY KEY (event_id, hour_start, route_id, stagnation_bucket);

INSERT INTO arc_stagnation_hourly (
    event_id, hour_start, route_id, stagnation_bucket, sample_count, stagnation_sum,
    flow_sum, flow_square_sum, flow_stagnation_sum, first_start, last_end)
SELECT event_id, hour_start, route_id, stagnation_bucket, sample_count, stagnation_sum,
    flow_sum, flow_square_sum, flow_stagnation_sum, first_start, last_end
FROM arc_stagnation_hourly_merged;

DROP TABLE arc_stagnation_hourly_merged;
