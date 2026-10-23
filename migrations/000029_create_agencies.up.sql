CREATE TABLE agencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agency_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agency_id UUID NOT NULL REFERENCES agencies(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agency_id, user_id)
);

ALTER TABLE workspaces ADD COLUMN agency_id UUID REFERENCES agencies(id) ON DELETE SET NULL;
ALTER TABLE workspaces ADD COLUMN client_label TEXT;
CREATE INDEX workspaces_agency_idx ON workspaces (agency_id);

ALTER TABLE api_keys ADD COLUMN agency_id UUID REFERENCES agencies(id) ON DELETE CASCADE;
CREATE INDEX api_keys_agency_idx ON api_keys (agency_id);

-- Backfill: one agency per existing workspace owner, with all their workspaces
-- attached as clients. Existing API keys inherit the agency for client scoping.
INSERT INTO agencies (owner_user_id, name)
SELECT DISTINCT ON (w.user_id) w.user_id, COALESCE(NULLIF(w.business_name, ''), 'My Agency')
FROM workspaces w
ORDER BY w.user_id, w.created_at ASC;

INSERT INTO agency_members (agency_id, user_id, role)
SELECT a.id, a.owner_user_id, 'owner'
FROM agencies a
ON CONFLICT (agency_id, user_id) DO NOTHING;

UPDATE workspaces w
SET agency_id = a.id
FROM agencies a
WHERE a.owner_user_id = w.user_id AND w.agency_id IS NULL;

UPDATE api_keys k
SET agency_id = w.agency_id
FROM workspaces w
WHERE k.workspace_id = w.id AND k.agency_id IS NULL;

ALTER TABLE sessions ADD COLUMN workspace_id UUID REFERENCES workspaces(id) ON DELETE CASCADE;
UPDATE sessions s
SET workspace_id = (
    SELECT w.id FROM workspaces w WHERE w.user_id = s.user_id ORDER BY w.created_at ASC LIMIT 1
);
