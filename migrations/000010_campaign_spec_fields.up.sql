ALTER TABLE campaigns
    ADD COLUMN start_date DATE,
    ADD COLUMN end_date DATE,
    ADD COLUMN cta TEXT,
    ADD COLUMN targeting JSONB;
