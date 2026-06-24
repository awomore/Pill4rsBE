ALTER TABLE campaigns
    DROP COLUMN IF EXISTS external_adset_id,
    DROP COLUMN IF EXISTS external_ad_id,
    DROP COLUMN IF EXISTS external_creative_id,
    DROP COLUMN IF EXISTS creative;
