CREATE TABLE proposals (
  id         text PRIMARY KEY,
  kind       text NOT NULL CHECK (kind IN ('edit', 'delete', 'move')),
  path       text NOT NULL,
  new_path   text,
  status     text NOT NULL CHECK (status IN ('pending', 'applied', 'rejected', 'stale', 'expired')),
  edits      jsonb,
  link_edits jsonb,
  reason     text,
  base_hash  text,
  result     jsonb,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  decided_at timestamptz,
  decided_by text,
  comment    text
);

CREATE INDEX proposals_status ON proposals (status, created_at);
