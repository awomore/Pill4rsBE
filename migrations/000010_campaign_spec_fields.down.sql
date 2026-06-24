ALTER TABLE campaigns
    DROP COLUMN IF EXISTS start_date,
    DROP COLUMN IF EXISTS end_date,
    DROP COLUMN IF EXISTS cta,
    DROP COLUMN IF EXISTS targeting;
