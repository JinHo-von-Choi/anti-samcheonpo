CREATE TABLE contract_record (
  session_id TEXT NOT NULL,
  checks_hash TEXT,
  state TEXT NOT NULL,
  recorded_at TEXT NOT NULL
);
CREATE INDEX contract_record_session ON contract_record(session_id);
