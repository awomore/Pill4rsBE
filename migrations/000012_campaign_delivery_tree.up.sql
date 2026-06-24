ALTER TABLE campaigns
    ADD COLUMN external_adset_id TEXT,
    ADD COLUMN external_ad_id TEXT,
    ADD COLUMN external_creative_id TEXT,
    ADD COLUMN creative JSONB;
