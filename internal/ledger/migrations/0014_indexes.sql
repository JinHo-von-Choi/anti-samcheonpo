-- The ledger holds one row per event, per verdict and per intervention, and
-- every query the CLI and the hooks run against it is by session. The
-- composite indexes below answer those queries from the index instead of
-- scanning the table.
--
-- verdict(session_id, rule) answers the per-rule counts a session report
-- and the rule joins in the lineage and outcome queries.
-- intervention(session_id, delivered_at) answers the delivery window of a
-- session's interventions, which is what the review queue reads.
-- event(session_id, seq) is the order the events of a session are read in
-- and the join key of every verdict to the event it points at.
-- session(started_at) answers the session list and the windowed scans over
-- the sessions of a period.
CREATE INDEX verdict_session_rule ON verdict(session_id, rule);
CREATE INDEX intervention_session_delivered ON intervention(session_id, delivered_at);
CREATE INDEX event_session_seq ON event(session_id, seq);
CREATE INDEX session_started_at ON session(started_at);
