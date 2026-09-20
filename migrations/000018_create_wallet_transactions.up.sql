CREATE TABLE wallet_transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    currency TEXT NOT NULL,
    kind TEXT NOT NULL,
    amount_minor BIGINT NOT NULL,
    balance_after_minor BIGINT NOT NULL,
    idempotency_key TEXT NOT NULL,
    source_type TEXT,
    source_id TEXT,
    fx_rate NUMERIC(20, 10),
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (wallet_id, idempotency_key)
);

CREATE INDEX wallet_transactions_wallet_created_idx
    ON wallet_transactions (wallet_id, created_at DESC);
