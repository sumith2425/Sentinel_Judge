CREATE TABLE IF NOT EXISTS problems (
    id VARCHAR(128) PRIMARY KEY,
    title VARCHAR(255) NOT NULL,
    category VARCHAR(128) NOT NULL DEFAULT 'General',
    difficulty VARCHAR(64) NOT NULL DEFAULT 'Standard',
    time_limit_ms INT NOT NULL DEFAULT 1000,
    memory_limit_mb INT NOT NULL DEFAULT 512,
    description TEXT NOT NULL,
    input_format TEXT NOT NULL,
    output_format TEXT NOT NULL,
    constraints JSONB NOT NULL DEFAULT '[]'::jsonb,
    examples JSONB NOT NULL DEFAULT '[]'::jsonb,
    starter_code JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_problems_category_id ON problems (category, id);