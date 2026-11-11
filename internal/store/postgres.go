package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sumith2425/sentinel-judge/internal/models"
)

type Store struct {
	db *sql.DB
}

func NewPostgresStore(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateSubmission(ctx context.Context, sub *models.Submission) error {
	query := `
		INSERT INTO submissions (
			submission_id, user_id, question_id, language, code,
			time_limit_ms, memory_limit_mb, status, retry_count, submitted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := s.db.ExecContext(
		ctx,
		query,
		sub.SubmissionID,
		sub.UserID,
		sub.QuestionID,
		sub.Language,
		sub.Code,
		sub.TimeLimitMS,
		sub.MemoryLimitMB,
		sub.Status,
		sub.RetryCount,
		sub.SubmittedAt,
	)
	return err
}

func (s *Store) UpdateStatus(ctx context.Context, id uuid.UUID, status models.SubmissionStatus) error {
	query := `UPDATE submissions SET status = $1 WHERE submission_id = $2`
	_, err := s.db.ExecContext(ctx, query, status, id)
	return err
}

func (s *Store) MarkProcessing(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE submissions 
		SET status = $1, started_at = NOW() 
		WHERE submission_id = $2
	`
	_, err := s.db.ExecContext(ctx, query, models.StatusProcessing, id)
	return err
}

func (s *Store) SaveFinalVerdict(
	ctx context.Context,
	id uuid.UUID,
	verdict models.SubmissionStatus,
	details *string,
	execTimeMs *int,
	execMemoryKb *int,
) error {
	query := `
		UPDATE submissions
		SET status = $1,
		    verdict_details = $2,
		    execution_time_ms = $3,
		    execution_memory_kb = $4,
		    completed_at = NOW()
		WHERE submission_id = $5
	`
	_, err := s.db.ExecContext(ctx, query, verdict, details, execTimeMs, execMemoryKb, id)
	return err
}

func (s *Store) GetSubmission(ctx context.Context, id uuid.UUID) (*models.Submission, error) {
	query := `
		SELECT 
			submission_id, user_id, question_id, language, code,
			time_limit_ms, memory_limit_mb, status, verdict_details,
			execution_time_ms, execution_memory_kb, retry_count,
			submitted_at, started_at, completed_at
		FROM submissions
		WHERE submission_id = $1
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var sub models.Submission
	err := row.Scan(
		&sub.SubmissionID,
		&sub.UserID,
		&sub.QuestionID,
		&sub.Language,
		&sub.Code,
		&sub.TimeLimitMS,
		&sub.MemoryLimitMB,
		&sub.Status,
		&sub.VerdictDetails,
		&sub.ExecutionTimeMS,
		&sub.ExecutionMemoryKB,
		&sub.RetryCount,
		&sub.SubmittedAt,
		&sub.StartedAt,
		&sub.CompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Store) GetStalePendingSubmissions(ctx context.Context, olderThan time.Duration) ([]models.Submission, error) {
	query := `
		SELECT 
			submission_id, user_id, question_id, language, code,
			time_limit_ms, memory_limit_mb, status, verdict_details,
			execution_time_ms, execution_memory_kb, retry_count,
			submitted_at, started_at, completed_at
		FROM submissions
		WHERE status = $1 AND submitted_at < NOW() - $2::interval
	`
	intervalStr := olderThan.String()
	return s.querySubmissions(ctx, query, models.StatusPendingQueue, intervalStr)
}

func (s *Store) GetAllQueuedSubmissions(ctx context.Context) ([]models.Submission, error) {
	query := `
		SELECT 
			submission_id, user_id, question_id, language, code,
			time_limit_ms, memory_limit_mb, status, verdict_details,
			execution_time_ms, execution_memory_kb, retry_count,
			submitted_at, started_at, completed_at
		FROM submissions
		WHERE status = $1
		ORDER BY submitted_at ASC
	`
	return s.querySubmissions(ctx, query, models.StatusQueued)
}

func (s *Store) GetProcessingSubmissions(ctx context.Context) ([]models.Submission, error) {
	query := `
		SELECT 
			submission_id, user_id, question_id, language, code,
			time_limit_ms, memory_limit_mb, status, verdict_details,
			execution_time_ms, execution_memory_kb, retry_count,
			submitted_at, started_at, completed_at
		FROM submissions
		WHERE status = $1
	`
	return s.querySubmissions(ctx, query, models.StatusProcessing)
}

func (s *Store) IncrementRetryAndRequeue(ctx context.Context, id uuid.UUID) (int, error) {
	query := `
		UPDATE submissions
		SET retry_count = retry_count + 1,
		    status = $1,
		    started_at = NULL
		WHERE submission_id = $2
		RETURNING retry_count
	`
	var newCount int
	err := s.db.QueryRowContext(ctx, query, models.StatusQueued, id).Scan(&newCount)
	return newCount, err
}

func (s *Store) querySubmissions(ctx context.Context, query string, args ...any) ([]models.Submission, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.Submission
	for rows.Next() {
		var sub models.Submission
		if err := rows.Scan(
			&sub.SubmissionID,
			&sub.UserID,
			&sub.QuestionID,
			&sub.Language,
			&sub.Code,
			&sub.TimeLimitMS,
			&sub.MemoryLimitMB,
			&sub.Status,
			&sub.VerdictDetails,
			&sub.ExecutionTimeMS,
			&sub.ExecutionMemoryKB,
			&sub.RetryCount,
			&sub.SubmittedAt,
			&sub.StartedAt,
			&sub.CompletedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, sub)
	}
	return results, rows.Err()
}