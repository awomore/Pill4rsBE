ALTER TABLE ad_accounts DROP CONSTRAINT IF EXISTS ad_accounts_workspace_platform_account_key;
ALTER TABLE ad_accounts ADD CONSTRAINT ad_accounts_workspace_platform_key
    UNIQUE (workspace_id, platform);

ALTER TABLE ad_accounts DROP COLUMN IF EXISTS name;
