CREATE TABLE greetings (
    id BIGSERIAL PRIMARY KEY,
    tenant_public_id TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX greetings_tenant_public_id_idx ON greetings (tenant_public_id);
