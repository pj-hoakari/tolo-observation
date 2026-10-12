CREATE TABLE observation_settings (
    event_id TEXT PRIMARY KEY,
    tenant_public_id TEXT NOT NULL,
    history_window_days INTEGER NOT NULL CHECK (history_window_days >= 1),
    event_type TEXT NOT NULL CHECK (event_type IN ('short_term', 'long_term')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
