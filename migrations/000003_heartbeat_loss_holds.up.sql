CREATE TABLE heartbeat_loss_holds (
    id BIGSERIAL PRIMARY KEY,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    device_count INTEGER NOT NULL,
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX heartbeat_loss_holds_open_idx ON heartbeat_loss_holds ((true)) WHERE ended_at IS NULL;
