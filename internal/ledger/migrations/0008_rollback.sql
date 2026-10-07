CREATE TABLE rollback (
  session_id TEXT NOT NULL,
  at TEXT NOT NULL,
  basis_seq INTEGER NOT NULL,
  restored TEXT NOT NULL,
  skipped TEXT NOT NULL,
  failed TEXT NOT NULL
);
