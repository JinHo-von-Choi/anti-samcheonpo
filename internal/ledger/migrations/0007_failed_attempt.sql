CREATE TABLE failed_attempt (
  task_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  edit_fp TEXT NOT NULL,
  path TEXT NOT NULL,
  failure_fp TEXT NOT NULL,
  session_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  seq INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (task_id, revision, edit_fp, failure_fp)
);
