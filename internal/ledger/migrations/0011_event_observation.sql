CREATE TABLE event_observation (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY(session_id, seq)
);
