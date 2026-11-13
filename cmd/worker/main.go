package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sumith2425/sentinel-judge/internal/models"
	"github.com/sumith2425/sentinel-judge/internal/queue"
	"github.com/sumith2425/sentinel-judge/internal/sandbox"
	"github.com/sumith2425/sentinel-judge/internal/store"
)

func main() {
	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://sentinel:sentinel_pass@localhost:5432/sentinel_judge?sslmode=disable"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	problemsDir := os.Getenv("PROBLEMS_DIR")
	if problemsDir == "" {
		problemsDir = "./testdata/problems"
	}

	workDir := os.Getenv("WORK_DIR")
	if workDir == "" {
		workDir = "/tmp/sentinel-work"
	}

	pgStore, err := store.NewPostgresStore(pgDSN)
	if err != nil {
		log.Fatalf("Failed to initialize postgres store: %v", err)
	}
	defer pgStore.Close()

	rQueue, err := queue.NewRedisClient(redisAddr, "", 0)
	if err != nil {
		log.Fatalf("Failed to initialize redis client: %v", err)
	}
	defer rQueue.Close()

	runner := sandbox.NewRunner(problemsDir, workDir)

	loopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("Worker started. Waiting for submissions...")

	for {
		select {
		case <-loopCtx.Done():
			log.Println("Shutdown signal received. Exiting worker loop cleanly.")
			return
		default:
		}

		sub, err := rQueue.PopSubmission(loopCtx, 2*time.Second)
		if err != nil {
			if loopCtx.Err() != nil {
				log.Println("Shutdown signal received during pop. Exiting worker.")
				return
			}
			log.Printf("Error popping submission from Redis: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		if sub == nil {
			continue
		}

		processSubmission(pgStore, rQueue, runner, sub)
	}
}

func processSubmission(
	pgStore *store.Store,
	rQueue *queue.RedisClient,
	runner *sandbox.Runner,
	sub *models.Submission,
) {
	jobCtx := context.Background()
	log.Printf("Processing submission %s (user=%s, problem=%s, lang=%s)",
		sub.SubmissionID, sub.UserID, sub.QuestionID, sub.Language)

	if err := pgStore.MarkProcessing(jobCtx, sub.SubmissionID); err != nil {
		log.Printf("Failed to mark submission %s as PROCESSING: %v", sub.SubmissionID, err)
		return
	}

	onProgress := func(status models.SubmissionStatus, currentTest, totalTests int) {
		update := &models.LiveStatusUpdate{
			SubmissionID:  sub.SubmissionID,
			Status:        status,
			CurrentTest:   currentTest,
			TotalTests:    totalTests,
			LastHeartbeat: time.Now().UTC(),
		}
		if err := rQueue.SetLiveStatus(jobCtx, update); err != nil {
			log.Printf("Failed to publish live status for %s: %v", sub.SubmissionID, err)
		}
	}

	result := runner.Evaluate(jobCtx, sub, onProgress)

	var memPtr *int
	if result.ExecutionMemoryKB > 0 {
		memVal := result.ExecutionMemoryKB
		memPtr = &memVal
	}
	timeVal := result.ExecutionTimeMS

	if err := pgStore.SaveFinalVerdict(
		jobCtx,
		sub.SubmissionID,
		result.Verdict,
		result.VerdictDetails,
		&timeVal,
		memPtr,
	); err != nil {
		log.Printf("Failed to save final verdict to PostgreSQL for %s: %v", sub.SubmissionID, err)
		return
	}

	finalUpdate := &models.LiveStatusUpdate{
		SubmissionID:      sub.SubmissionID,
		Status:            result.Verdict,
		VerdictDetails:    result.VerdictDetails,
		ExecutionTimeMS:   &timeVal,
		ExecutionMemoryKB: memPtr,
		LastHeartbeat:     time.Now().UTC(),
	}
	if err := rQueue.SetLiveStatus(jobCtx, finalUpdate); err != nil {
		log.Printf("Failed to publish final live status for %s: %v", sub.SubmissionID, err)
	}

	log.Printf("Completed submission %s -> Verdict: %s (%dms)",
		sub.SubmissionID, result.Verdict, result.ExecutionTimeMS)
}