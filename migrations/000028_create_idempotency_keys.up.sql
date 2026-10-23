CREATE TABLE idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'in_progress',
    response_status INTEGER,
    response_body BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, endpoint, key)
);

CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);
