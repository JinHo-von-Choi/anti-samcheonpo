-- The intervention table must describe itself. It used to reach into the
-- verdict table for the rule name, but the verdict table is rewritten on
-- every analysis, so intervention rows outlived the verdicts they pointed
-- at and a later count lost the rules it could not join.
ALTER TABLE intervention ADD COLUMN rule TEXT NOT NULL DEFAULT '';

-- A block is an action, not a channel. The agent channel carries both a
-- delivered advice and a stopped call, so the channel alone cannot say
-- which happened. The blocked column records the action directly.
ALTER TABLE intervention ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0;
