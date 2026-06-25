-- Schema for summarize app database

CREATE TABLE IF NOT EXISTS summary_runs (
  id TEXT PRIMARY KEY,

  status TEXT NOT NULL CHECK (
    status IN ('queued', 'running', 'succeeded', 'failed')
  ),

  stage TEXT NOT NULL DEFAULT 'queued',

  input_type TEXT NOT NULL CHECK (
    input_type IN ('youtube', 'text')
  ),

  source_url TEXT,
  input_text TEXT,

  youtube_video_id TEXT,
  youtube_title TEXT,

  engine TEXT NOT NULL CHECK (
    engine IN ('pi', 'agy')
  ),

  model TEXT,
  format TEXT NOT NULL DEFAULT 'summary',
  truncated BOOLEAN NOT NULL DEFAULT 0,
  transcript_with_timestamps TEXT,
  transcript_with_timestamps_chars INTEGER NOT NULL DEFAULT 0,

  prompt TEXT NOT NULL,

  transcript TEXT,
  transcript_chars INTEGER NOT NULL DEFAULT 0,

  summary TEXT,
  summary_chars INTEGER NOT NULL DEFAULT 0,

  error_code TEXT,
  error_message TEXT,

  stderr TEXT,
  command TEXT,

  event_id TEXT,
  event_published_at TEXT,

  workflow_instance_id TEXT,
  workflow_execution_id TEXT,

  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT
);


CREATE INDEX IF NOT EXISTS idx_summary_runs_status
  ON summary_runs(status);

CREATE INDEX IF NOT EXISTS idx_summary_runs_created_at
  ON summary_runs(created_at);

CREATE INDEX IF NOT EXISTS idx_summary_runs_workflow_instance_id
  ON summary_runs(workflow_instance_id);
