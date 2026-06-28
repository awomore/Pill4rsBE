ALTER TABLE ad_accounts ADD COLUMN name TEXT;

ALTER TABLE ad_accounts DROP CONSTRAINT IF EXISTS ad_accounts_workspace_platform_key;
ALTER TABLE ad_accounts ADD CONSTRAINT ad_accounts_workspace_platform_account_key
    UNIQUE (workspace_id, platform, external_account_id);
