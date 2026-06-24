ALTER TABLE ad_accounts
    DROP COLUMN IF EXISTS page_id,
    DROP COLUMN IF EXISTS pixel_id;
