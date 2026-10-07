-- These identities deliberately do not reference legacy session rows, which
-- are replaced during retrospective re-analysis. Composite agent/session keys
-- avoid accidental collisions between providers.
CREATE TABLE task (
  id TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  project_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE intent_revision (
  task_id TEXT NOT NULL REFERENCES task(id),
  revision INTEGER NOT NULL CHECK(revision > 0),
  payload TEXT NOT NULL,
  PRIMARY KEY(task_id, revision)
);
CREATE TABLE task_session (
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL,
  task_id TEXT NOT NULL REFERENCES task(id),
  payload TEXT NOT NULL,
  PRIMARY KEY(agent, session_id)
);
CREATE INDEX task_session_task ON task_session(task_id);
CREATE TABLE verification_evidence (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  key_digest TEXT NOT NULL,
  payload TEXT NOT NULL,
  FOREIGN KEY(task_id, revision) REFERENCES intent_revision(task_id, revision)
);
CREATE INDEX verification_evidence_key ON verification_evidence(key_digest);
CREATE TABLE policy_decision (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES task(id),
  payload TEXT NOT NULL
);
