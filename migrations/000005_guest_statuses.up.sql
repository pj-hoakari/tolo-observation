CREATE TABLE guest_statuses (
    event_id TEXT PRIMARY KEY,
    tenant_public_id TEXT NOT NULL,
    sequence BIGINT NOT NULL,
    taken_at TIMESTAMPTZ NOT NULL,
    crowd JSONB NOT NULL,
    queues JSONB NOT NULL
);
