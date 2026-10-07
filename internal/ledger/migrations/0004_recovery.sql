ALTER TABLE intervention ADD COLUMN delivery_state TEXT NOT NULL DEFAULT 'legacy_unknown';
ALTER TABLE intervention ADD COLUMN emitted_at TEXT;
CREATE TABLE recovery_attempt (
  id TEXT PRIMARY KEY,
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  payload TEXT NOT NULL
);
CREATE INDEX recovery_attempt_session ON recovery_attempt(agent, session_id);
