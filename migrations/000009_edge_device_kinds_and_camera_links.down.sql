DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM edge_devices WHERE kind = 'camera') THEN
        RAISE EXCEPTION 'cannot downgrade while camera edge devices exist';
    END IF;
END $$;

DROP TABLE IF EXISTS camera_links;
ALTER TABLE edge_devices DROP CONSTRAINT IF EXISTS edge_devices_kind_check;
ALTER TABLE edge_devices DROP COLUMN IF EXISTS kind;
