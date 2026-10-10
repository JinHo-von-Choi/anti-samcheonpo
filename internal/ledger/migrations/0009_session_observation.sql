-- Observation metadata is outside the canonical seal. Legacy rows remain
-- unknown; daemon shutdown is not a synthetic agent SessionEnd.
CREATE TABLE session_observation (
  session_id TEXT PRIMARY KEY,
  agent TEXT NOT NULL,
  last_activity_at TEXT,
  closed_at TEXT,
  close_reason TEXT NOT NULL,
  evaluator_version TEXT NOT NULL
);
