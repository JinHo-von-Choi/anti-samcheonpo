-- Completed analysis snapshots are independent of mutable live event rows.
-- Legacy analyses have no snapshot and retain unknown observation coverage.
CREATE TABLE receipt_snapshot (
  session_id TEXT PRIMARY KEY,
  payload TEXT NOT NULL
);
