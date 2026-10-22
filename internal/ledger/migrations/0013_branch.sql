-- A fork keeps the failed session and starts a new one from a recorded
-- passing point. The old session records that it diverged, so a receipt can
-- tell a branch from a session that simply ended.
ALTER TABLE session ADD COLUMN branch_state TEXT NOT NULL DEFAULT '';
