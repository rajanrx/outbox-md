CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  current_version_id TEXT,
  status TEXT NOT NULL DEFAULT 'draft',
  approved_version_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS versions (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  ordinal INTEGER NOT NULL,
  content TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS comments (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  against_version_id TEXT NOT NULL REFERENCES versions(id),
  anchor_start INTEGER NOT NULL,
  anchor_end INTEGER NOT NULL,
  author_identity TEXT NOT NULL,
  owner TEXT NOT NULL,
  status TEXT NOT NULL,
  claim_token TEXT NOT NULL DEFAULT '',
  post_approval INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS suggestions (
  id TEXT PRIMARY KEY,
  comment_id TEXT NOT NULL REFERENCES comments(id),
  against_version_id TEXT NOT NULL REFERENCES versions(id),
  proposed_content TEXT NOT NULL,
  state TEXT NOT NULL,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS thread_messages (
  id TEXT PRIMARY KEY,
  comment_id TEXT NOT NULL REFERENCES comments(id),
  author_identity TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS approvals (
  id TEXT PRIMARY KEY,
  doc_id TEXT NOT NULL REFERENCES documents(id),
  version_id TEXT NOT NULL REFERENCES versions(id),
  approved_by TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
-- AI Council (roadmap §3): N candidates + a synthesis per comment, additive.
CREATE TABLE IF NOT EXISTS candidate_sets (
  id TEXT PRIMARY KEY,
  comment_id TEXT NOT NULL UNIQUE REFERENCES comments(id),
  state TEXT NOT NULL DEFAULT 'gathering',
  quorum INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  candidate_set_id TEXT NOT NULL REFERENCES candidate_sets(id),
  lens TEXT NOT NULL,
  verdict TEXT NOT NULL,
  rationale TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  agent_identity TEXT NOT NULL,
  chosen INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS syntheses (
  id TEXT PRIMARY KEY,
  candidate_set_id TEXT NOT NULL REFERENCES candidate_sets(id),
  agreement_score REAL NOT NULL DEFAULT 0,
  dissent TEXT NOT NULL DEFAULT '',
  suggestion_id TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
