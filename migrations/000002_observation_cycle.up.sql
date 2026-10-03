CREATE TABLE snapshots (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id TEXT NOT NULL UNIQUE,
    tenant_public_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    scores JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX snapshots_event_id_idx ON snapshots (event_id);

CREATE TABLE optimization_results (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL,
    snapshot_id TEXT NOT NULL,
    verdict TEXT NOT NULL,
    detection_state JSONB NOT NULL,
    optimization_result JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX optimization_results_event_id_idx ON optimization_results (event_id);
