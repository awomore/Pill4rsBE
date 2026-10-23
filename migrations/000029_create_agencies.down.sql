ALTER TABLE sessions DROP COLUMN IF EXISTS workspace_id;
ALTER TABLE api_keys DROP COLUMN IF EXISTS agency_id;
ALTER TABLE workspaces DROP COLUMN IF EXISTS client_label;
ALTER TABLE workspaces DROP COLUMN IF EXISTS agency_id;
DROP TABLE IF EXISTS agency_members;
DROP TABLE IF EXISTS agencies;
