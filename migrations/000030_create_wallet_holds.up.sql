CREATE TABLE wallet_holds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    currency TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    remaining_minor BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    source_type TEXT,
    source_id TEXT,
    idempotency_key TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, idempotency_key)
);

CREATE INDEX wallet_holds_active_idx ON wallet_holds (wallet_id, currency, status);
CREATE INDEX wallet_holds_source_idx ON wallet_holds (workspace_id, source_type, source_id, status);
