ALTER TABLE campaigns
    ADD COLUMN bid_strategy TEXT NOT NULL DEFAULT 'LOWEST_COST_WITHOUT_CAP',
    ADD COLUMN bid_cap NUMERIC,
    ADD COLUMN pacing_type TEXT NOT NULL DEFAULT 'standard',
    ADD COLUMN frequency_cap INTEGER,
    ADD COLUMN frequency_cap_time_unit TEXT,
    ADD COLUMN variants JSONB,
    ADD COLUMN provenance JSONB;
