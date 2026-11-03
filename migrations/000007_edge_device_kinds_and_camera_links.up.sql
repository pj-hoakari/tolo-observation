ALTER TABLE edge_devices ADD COLUMN kind TEXT NOT NULL DEFAULT 'observation_page';
ALTER TABLE edge_devices ADD CONSTRAINT edge_devices_kind_check
    CHECK (kind IN ('observation_page', 'camera'));

CREATE TABLE camera_links (
    id BIGSERIAL PRIMARY KEY,
    camera_edge_device_id BIGINT NOT NULL UNIQUE REFERENCES edge_devices (id),
    observation_point_id BIGINT NOT NULL UNIQUE REFERENCES observation_points (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
