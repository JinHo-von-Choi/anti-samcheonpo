-- A scolding user message marks the request span before it. Only the themes
-- and a hash of the message are kept; the message itself is not stored.
CREATE TABLE reproach (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  start_seq INTEGER NOT NULL,
  agent TEXT NOT NULL,
  project_path TEXT NOT NULL DEFAULT '',
  basis TEXT NOT NULL,
  themes TEXT NOT NULL DEFAULT '[]',
  text_hash TEXT NOT NULL,
  matched TEXT NOT NULL DEFAULT '[]',
  missed TEXT NOT NULL DEFAULT '[]',
  done_claimed INTEGER NOT NULL DEFAULT 0,
  span_micro_krw INTEGER NOT NULL DEFAULT 0,
  span_tokens INTEGER NOT NULL DEFAULT 0,
  rework_micro_krw INTEGER NOT NULL DEFAULT 0,
  rework_tokens INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  PRIMARY KEY(session_id, seq)
);
CREATE INDEX reproach_created ON reproach(created_at);

-- How many user messages a session's reproach count is out of. observed is 0
-- when the session gave no user input to read (a scheduled run).
CREATE TABLE reproach_session (
  session_id TEXT PRIMARY KEY,
  agent TEXT NOT NULL,
  basis TEXT NOT NULL,
  user_messages INTEGER NOT NULL DEFAULT 0,
  observed INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL
);
CREATE INDEX reproach_session_started ON reproach_session(started_at);

-- A correction the user gave the agent, kept as an executable rule
-- candidate. statement is the extracted rule sentence; evidence keeps only
-- message hashes.
CREATE TABLE correction (
  id TEXT PRIMARY KEY,
  project_path TEXT NOT NULL,
  kind TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  persistent INTEGER NOT NULL DEFAULT 0,
  statement TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '[]',
  sessions TEXT NOT NULL DEFAULT '[]',
  state TEXT NOT NULL,
  violations INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  approved_at TEXT
);
CREATE INDEX correction_project ON correction(project_path, state);

-- A stable identity of a scheduled job, so its runs can be compared.
ALTER TABLE session ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
CREATE INDEX session_job ON session(job_id) WHERE job_id != '';
