CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS submissions (
    submission_id UUID PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL,
    question_id VARCHAR(64) NOT NULL,
    language VARCHAR(16) NOT NULL,
    code TEXT NOT NULL,
    time_limit_ms INT NOT NULL,
    memory_limit_mb INT NOT NULL,
    status VARCHAR(32) NOT NULL,
    verdict_details TEXT,
    execution_time_ms INT,
    execution_memory_kb INT,
    retry_count INT NOT NULL DEFAULT 0,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_submissions_status_submitted_at 
ON submissions (status, submitted_at);