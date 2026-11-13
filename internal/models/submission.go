package models

import (
	"time"

	"github.com/google/uuid"
)

type SubmissionStatus string

const (
	StatusPendingQueue        SubmissionStatus = "PENDING_QUEUE"
	StatusQueued              SubmissionStatus = "QUEUED"
	StatusProcessing          SubmissionStatus = "PROCESSING"
	StatusCompiling           SubmissionStatus = "COMPILING"
	StatusRunning             SubmissionStatus = "RUNNING"
	StatusAccepted            SubmissionStatus = "ACCEPTED"
	StatusWrongAnswer         SubmissionStatus = "WRONG_ANSWER"
	StatusTimeLimitExceeded   SubmissionStatus = "TIME_LIMIT_EXCEEDED"
	StatusMemoryLimitExceeded SubmissionStatus = "MEMORY_LIMIT_EXCEEDED"
	StatusCompilationError    SubmissionStatus = "COMPILATION_ERROR"
	StatusRuntimeError        SubmissionStatus = "RUNTIME_ERROR"
	StatusInternalError       SubmissionStatus = "INTERNAL_ERROR"
)

func IsTerminalStatus(s SubmissionStatus) bool {
	switch s {
	case StatusAccepted,
		StatusWrongAnswer,
		StatusTimeLimitExceeded,
		StatusMemoryLimitExceeded,
		StatusCompilationError,
		StatusRuntimeError,
		StatusInternalError:
		return true
	default:
		return false
	}
}

type Language string

const (
	LangCPP    Language = "cpp"
	LangPython Language = "python"
	LangGo     Language = "go"
)

type SubmissionRequest struct {
	UserID        string   `json:"user_id"`
	QuestionID    string   `json:"question_id"`
	Language      Language `json:"language"`
	Code          string   `json:"code"`
	TimeLimitMS   int      `json:"time_limit_ms"`
	MemoryLimitMB int      `json:"memory_limit_mb"`
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
	SubmissionID      uuid.UUID        `json:"submission_id"`
	Status            SubmissionStatus `json:"status"`
	CurrentTest       int              `json:"current_test"`
	TotalTests        int              `json:"total_tests"`
	VerdictDetails    *string          `json:"verdict_details,omitempty"`
	ExecutionTimeMS   *int             `json:"execution_time_ms,omitempty"`
	ExecutionMemoryKB *int             `json:"execution_memory_kb,omitempty"`
	LastHeartbeat     time.Time        `json:"last_heartbeat"`
}

type ProblemExample struct {
	Input       string `json:"input"`
	Output      string `json:"output"`
	Explanation string `json:"explanation,omitempty"`
}

type ProblemSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Category      string `json:"category"`
	Difficulty    string `json:"difficulty"`
	DisplayOrder  int    `json:"display_order"`
	TestCount     int    `json:"test_count"`
	TimeLimitMS   int    `json:"time_limit_ms"`
	MemoryLimitMB int    `json:"memory_limit_mb"`
}

type Problem struct {
	ID            string              `json:"id"`
	Title         string              `json:"title"`
	Category      string              `json:"category"`
	Difficulty    string              `json:"difficulty"`
	DisplayOrder  int                 `json:"display_order"`
	TestCount     int                 `json:"test_count"`
	TimeLimitMS   int                 `json:"time_limit_ms"`
	MemoryLimitMB int                 `json:"memory_limit_mb"`
	Description   string              `json:"description"`
	InputFormat   string              `json:"input_format"`
	OutputFormat  string              `json:"output_format"`
	Constraints   []string            `json:"constraints"`
	Examples      []ProblemExample    `json:"examples"`
	StarterCode   map[Language]string `json:"starter_code"`
}