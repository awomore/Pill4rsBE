ALTER TABLE ad_accounts DROP COLUMN IF EXISTS spend_limit_minor;
ALTER TABLE ad_accounts DROP COLUMN IF EXISTS currency;
ALTER TABLE ad_accounts DROP COLUMN IF EXISTS payment_instrument;
ALTER TABLE ad_accounts DROP COLUMN IF EXISTS payer;
ALTER TABLE ad_accounts DROP COLUMN IF EXISTS billing_mode;
