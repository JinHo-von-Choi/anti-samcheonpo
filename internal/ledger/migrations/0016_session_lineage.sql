-- Native host/SDK parent identity is independent of task goals and approvals.
CREATE TABLE session_lineage (
    agent TEXT NOT NULL,
    session_id TEXT NOT NULL,
    parent_session_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    source TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    PRIMARY KEY(agent, session_id)
);
CREATE INDEX idx_native_lineage_project ON session_lineage(agent,project_id);
