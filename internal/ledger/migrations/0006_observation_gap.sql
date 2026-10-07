CREATE TABLE observation_gap (
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL,
  rejected INTEGER NOT NULL CHECK (rejected > 0),
  PRIMARY KEY (agent, session_id)
);
