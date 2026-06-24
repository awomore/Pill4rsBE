ALTER TABLE ad_accounts
    ADD CONSTRAINT ad_accounts_workspace_platform_key UNIQUE (workspace_id, platform);
