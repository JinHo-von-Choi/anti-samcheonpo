ALTER TABLE intervention ADD COLUMN intervention_id TEXT NOT NULL DEFAULT '';
ALTER TABLE intervention ADD COLUMN metadata TEXT NOT NULL DEFAULT '{}';
UPDATE intervention SET intervention_id = 'legacy:' || rowid;
CREATE UNIQUE INDEX intervention_identity ON intervention(intervention_id) WHERE intervention_id != '';
