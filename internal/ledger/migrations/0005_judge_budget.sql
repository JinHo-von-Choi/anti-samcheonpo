-- Reservations must survive process failure, independently of replacement
-- of the legacy session/event rows during retrospective analysis.
CREATE TABLE judge_budget (
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL,
  version TEXT NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY(agent, session_id)
);
