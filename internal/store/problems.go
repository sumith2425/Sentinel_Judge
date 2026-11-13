package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sumith2425/sentinel-judge/internal/models"
)

func (s *Store) EnsureProblemSchema(ctx context.Context) error {
	query := `
	CREATE TABLE IF NOT EXISTS problems (
		id VARCHAR(128) PRIMARY KEY,
		title VARCHAR(255) NOT NULL,
		category VARCHAR(128) NOT NULL DEFAULT 'General',
		difficulty VARCHAR(64) NOT NULL DEFAULT 'Standard',
		display_order INT NOT NULL DEFAULT 1000,
		test_count INT NOT NULL DEFAULT 0,
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
	ALTER TABLE problems ADD COLUMN IF NOT EXISTS display_order INT NOT NULL DEFAULT 1000;
	ALTER TABLE problems ADD COLUMN IF NOT EXISTS test_count INT NOT NULL DEFAULT 0;
	CREATE INDEX IF NOT EXISTS idx_problems_order ON problems (display_order ASC, id ASC);
	`
	_, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to ensure problems schema: %w", err)
	}
	return nil
}

func (s *Store) UpsertProblem(ctx context.Context, p *models.Problem) error {
	constraintsJSON, err := json.Marshal(p.Constraints)
	if err != nil {
		return fmt.Errorf("failed to marshal constraints: %w", err)
	}
	examplesJSON, err := json.Marshal(p.Examples)
	if err != nil {
		return fmt.Errorf("failed to marshal examples: %w", err)
	}
	starterJSON, err := json.Marshal(p.StarterCode)
	if err != nil {
		return fmt.Errorf("failed to marshal starter_code: %w", err)
	}

	if p.Category == "" {
		p.Category = "General"
	}
	if p.Difficulty == "" {
		p.Difficulty = "Standard"
	}
	if p.DisplayOrder <= 0 {
		p.DisplayOrder = 1000
	}
	if p.TimeLimitMS <= 0 {
		p.TimeLimitMS = 1000
	}
	if p.MemoryLimitMB <= 0 {
		p.MemoryLimitMB = 512
	}

	query := `
	INSERT INTO problems (
		id, title, category, difficulty, display_order, test_count,
		time_limit_ms, memory_limit_mb, description, input_format,
		output_format, constraints, examples, starter_code
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	ON CONFLICT (id) DO UPDATE SET
		title = EXCLUDED.title,
		category = EXCLUDED.category,
		difficulty = EXCLUDED.difficulty,
		display_order = EXCLUDED.display_order,
		test_count = EXCLUDED.test_count,
		time_limit_ms = EXCLUDED.time_limit_ms,
		memory_limit_mb = EXCLUDED.memory_limit_mb,
		description = EXCLUDED.description,
		input_format = EXCLUDED.input_format,
		output_format = EXCLUDED.output_format,
		constraints = EXCLUDED.constraints,
		examples = EXCLUDED.examples,
		starter_code = EXCLUDED.starter_code
	`
	_, err = s.db.ExecContext(
		ctx, query,
		p.ID, p.Title, p.Category, p.Difficulty, p.DisplayOrder, p.TestCount,
		p.TimeLimitMS, p.MemoryLimitMB, p.Description, p.InputFormat,
		p.OutputFormat, constraintsJSON, examplesJSON, starterJSON,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert problem %s: %w", p.ID, err)
	}
	return nil
}

func (s *Store) ListProblems(ctx context.Context) ([]models.ProblemSummary, error) {
	query := `
	SELECT id, title, category, difficulty, display_order, test_count, time_limit_ms, memory_limit_mb
	FROM problems
	ORDER BY display_order ASC, id ASC
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list problems: %w", err)
	}
	defer rows.Close()

	var list []models.ProblemSummary
	for rows.Next() {
		var item models.ProblemSummary
		if err := rows.Scan(
			&item.ID, &item.Title, &item.Category, &item.Difficulty,
			&item.DisplayOrder, &item.TestCount, &item.TimeLimitMS, &item.MemoryLimitMB,
		); err != nil {
			return nil, fmt.Errorf("failed to scan problem summary: %w", err)
		}
		list = append(list, item)
	}
	if list == nil {
		list = []models.ProblemSummary{}
	}
	return list, rows.Err()
}

func (s *Store) GetProblem(ctx context.Context, id string) (*models.Problem, error) {
	query := `
	SELECT id, title, category, difficulty, display_order, test_count,
	       time_limit_ms, memory_limit_mb, description, input_format,
	       output_format, constraints, examples, starter_code
	FROM problems
	WHERE id = $1
	`
	var p models.Problem
	var constraintsRaw, examplesRaw, starterRaw []byte

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Title, &p.Category, &p.Difficulty, &p.DisplayOrder, &p.TestCount,
		&p.TimeLimitMS, &p.MemoryLimitMB, &p.Description, &p.InputFormat,
		&p.OutputFormat, &constraintsRaw, &examplesRaw, &starterRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get problem %s: %w", id, err)
	}

	_ = json.Unmarshal(constraintsRaw, &p.Constraints)
	_ = json.Unmarshal(examplesRaw, &p.Examples)
	_ = json.Unmarshal(starterRaw, &p.StarterCode)

	if p.Constraints == nil {
		p.Constraints = []string{}
	}
	if p.Examples == nil {
		p.Examples = []models.ProblemExample{}
	}
	if p.StarterCode == nil {
		p.StarterCode = map[models.Language]string{}
	}

	return &p, nil
}