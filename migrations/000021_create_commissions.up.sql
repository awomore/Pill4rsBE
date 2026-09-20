CREATE TABLE commissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    ad_account_id UUID NOT NULL REFERENCES ad_accounts(id) ON DELETE CASCADE,
    snapshot_id UUID NOT NULL REFERENCES performance_snapshots(id) ON DELETE CASCADE,
    currency TEXT NOT NULL,
    base_minor BIGINT NOT NULL,
    rate_bps INTEGER NOT NULL DEFAULT 1000,
    commission_minor BIGINT NOT NULL,
    fx_rate NUMERIC(20, 10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (snapshot_id)
);

CREATE INDEX commissions_workspace_created_idx
    ON commissions (workspace_id, created_at DESC);
