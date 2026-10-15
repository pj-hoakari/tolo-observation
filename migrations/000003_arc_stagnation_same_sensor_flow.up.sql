ALTER TABLE arc_stagnation_hourly ADD COLUMN same_sensor_flow BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE arc_stagnation_hourly DROP CONSTRAINT arc_stagnation_hourly_pkey;

ALTER TABLE arc_stagnation_hourly
    ADD PRIMARY KEY (event_id, hour_start, route_id, stagnation_bucket, same_sensor_flow);
