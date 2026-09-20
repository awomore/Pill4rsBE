ALTER TABLE ad_accounts ADD COLUMN billing_mode TEXT NOT NULL DEFAULT 'byob';
ALTER TABLE ad_accounts ADD COLUMN payer TEXT NOT NULL DEFAULT 'agency';
ALTER TABLE ad_accounts ADD COLUMN payment_instrument TEXT;
ALTER TABLE ad_accounts ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD';
ALTER TABLE ad_accounts ADD COLUMN spend_limit_minor BIGINT;
