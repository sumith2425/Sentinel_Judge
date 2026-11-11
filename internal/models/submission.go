package models

import (
	"time"

	"github.com/google/uuid"
)

type SubmissionStatus string

const (
	StatusPendingQueue        SubmissionStatus = "PENDING_QUEUE"
	StatusQueued              SubmissionStatus = "QUEUED"
	StatusProcessing           SubmissionStatus = "PROCESSING"
	StatusCompiling           SubmissionStatus = "COMPILING"
	StatusRunning             SubmissionStatus = "RUNNING"
	StatusAccepted            SubmissionStatus = "ACCEPTED"
	StatusTimeLimitExceeded   SubmissionStatus = "TIME_LIMIT_EXCEEDED"
	StatusMemoryLimitExceeded SubmissionStatus = "MEMORY_LIMIT_EXCEEDED"
	StatusRuntimeError        SubmissionStatus = "RUNTIME_ERROR"
	StatusCompilationError    SubmissionStatus = "COMPILATION_ERROR"
	StatusInternalError       SubmissionStatus = "INTERNAL_ERROR"
)

type Language string

const (
	LangCPP    Language = "cpp"
	LangPython Language = "python"
	LangGo     Language = "go"
)

type SubmissionRequest struct {
	UserID         string   `json:"user_id"`
	QuestionID     string   `json:"question_id"`
	Language       Language `json:"language"`
	Code           string   `json:"code"`
	TimeLimitMS    int      `json:"time_limit_ms"`
	MemoryLimitMB  int      `json:"memory_limit_mb"`
}

type Submission struct {
	SubmissionID      uuid.UUID        `json:"submission_id" db:"submission_id"`
	UserID            string           `json:"user_id" db:"user_id"`
	QuestionID        string           `json:"question_id" db:"question_id"`
	Language          Language         `json:"language" db:"language"`
	Code              string           `json:"code" db:"code"`
	TimeLimitMS       int              `json:"time_limit_ms" db:"time_limit_ms"`
	MemoryLimitMB     int              `json:"memory_limit_mb" db:"memory_limit_mb"`
	Status            SubmissionStatus `json:"status" db:"status"`
	VerdictDetails    *string          `json:"verdict_details,omitempty" db:"verdict_details"`
	ExecutionTimeMS   *int             `json:"execution_time_ms,omitempty" db:"execution_time_ms"`
	ExecutionMemoryKB *int             `json:"execution_memory_kb,omitempty" db:"execution_memory_kb"`
	RetryCount        int              `json:"retry_count" db:"retry_count"`
	SubmittedAt       time.Time        `json:"submitted_at" db:"submitted_at"`
	StartedAt         *time.Time       `json:"started_at,omitempty" db:"started_at"`
	CompletedAt       *time.Time       `json:"completed_at,omitempty" db:"completed_at"`
}

type LiveStatusUpdate struct {
	SubmissionID   uuid.UUID        `json:"submission_id"`
	Status         SubmissionStatus `json:"status"`
	CurrentTest    int              `json:"current_test"`
	TotalTests     int              `json:"total_tests"`
	VerdictDetails *string          `json:"verdict_details,omitempty"`
	LastHeartbeat  time.Time        `json:"last_heartbeat"`
}