DROP TABLE IF EXISTS camera_links;
ALTER TABLE edge_devices DROP CONSTRAINT IF EXISTS edge_devices_kind_check;
ALTER TABLE edge_devices DROP COLUMN IF EXISTS kind;
