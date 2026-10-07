CREATE TABLE session (
  id TEXT PRIMARY KEY,
  agent TEXT NOT NULL,
  model TEXT,
  project_path TEXT,
  contract_hash TEXT,
  started_at TEXT,
  ended_at TEXT,
  mode TEXT NOT NULL,
  source_path TEXT,
  source_hash TEXT,
  total_micro_krw INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  unpriced_tokens INTEGER NOT NULL DEFAULT 0,
  waste_micro_krw INTEGER NOT NULL DEFAULT 0,
  waste_tokens INTEGER NOT NULL DEFAULT 0,
  grade TEXT,
  format_ok INTEGER NOT NULL DEFAULT 1,
  quota_used_pct REAL,
  quota_window_min INTEGER,
  first_prompt_summary TEXT,
  analyzed_at TEXT
);
CREATE TABLE event (
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  ts TEXT,
  kind TEXT NOT NULL,
  tool TEXT,
  raw_tool TEXT,
  cmd_fp TEXT,
  paths TEXT,
  ws_before TEXT,
  ws_after TEXT,
  result_fp TEXT,
  exit_code INTEGER,
  tokens_in INTEGER, tokens_out INTEGER, tokens_cache_read INTEGER, tokens_cache_write INTEGER,
  model TEXT,
  cost_micro_krw INTEGER NOT NULL DEFAULT 0,
  priced INTEGER NOT NULL DEFAULT 1,
  category TEXT,
  category_basis TEXT,
  bucket TEXT,
  symptom TEXT,
  estimated INTEGER NOT NULL DEFAULT 0,
  forced INTEGER NOT NULL DEFAULT 0,
  parent INTEGER NOT NULL DEFAULT 0,
  summary TEXT,
  source_ref TEXT,
  chain_hash TEXT,
  UNIQUE(session_id, seq)
);
CREATE TABLE progress (
  session_id TEXT NOT NULL,
  event_seq INTEGER,
  ts TEXT,
  criteria_met INTEGER, criteria_total INTEGER,
  failing_tests INTEGER, error_count INTEGER, net_diff_lines INTEGER,
  detail TEXT
);
CREATE TABLE verdict (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  detector TEXT NOT NULL,
  rule TEXT NOT NULL,
  confidence REAL NOT NULL,
  evidence_event_ids TEXT NOT NULL,
  waste_micro_krw INTEGER NOT NULL DEFAULT 0,
  level INTEGER NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  suppressed INTEGER NOT NULL DEFAULT 0,
  estimate INTEGER NOT NULL DEFAULT 0,
  arm TEXT,
  facts TEXT,
  message_user TEXT,
  message_agent TEXT,
  chain_hash TEXT
);
CREATE TABLE intervention (
  verdict_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  channel TEXT NOT NULL,
  level INTEGER NOT NULL,
  arm TEXT,
  delivered_at TEXT,
  user_choice TEXT,
  outcome TEXT
);
CREATE TABLE feedback (
  verdict_id TEXT,
  session_id TEXT,
  rule TEXT,
  label TEXT NOT NULL,
  reason TEXT,
  note TEXT,
  project_path TEXT,
  created_at TEXT
);
CREATE TABLE price (
  model TEXT, input REAL, output REAL, cache_read REAL, cache_write REAL,
  currency TEXT, fx_rate REAL, valid_from TEXT, source TEXT
);
CREATE TABLE audit_source (
  path TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  agent TEXT NOT NULL,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL,
  eval_version TEXT NOT NULL
);
CREATE TABLE session_seal (
  session_id TEXT PRIMARY KEY,
  seal TEXT NOT NULL
);
CREATE INDEX event_session ON event(session_id);
CREATE INDEX verdict_session ON verdict(session_id);
CREATE INDEX intervention_session ON intervention(session_id);
