ALTER TABLE arc_stagnation_hourly
    DROP COLUMN IF EXISTS flow_sum,
    DROP COLUMN IF EXISTS flow_square_sum,
    DROP COLUMN IF EXISTS flow_stagnation_sum;
