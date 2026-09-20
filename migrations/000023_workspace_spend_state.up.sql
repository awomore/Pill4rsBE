ALTER TABLE workspaces ADD COLUMN spend_state TEXT NOT NULL DEFAULT 'active';
ALTER TABLE workspaces ADD COLUMN spend_state_reason TEXT;
ALTER TABLE workspaces ADD COLUMN commission_rate_bps INTEGER NOT NULL DEFAULT 1000;
