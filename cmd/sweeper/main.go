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
	"github.com/sumith2425/sentinel-judge/internal/store"
)

const MaxRetries = 3

func main() {
	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://sentinel:sentinel_pass@localhost:5432/sentinel_judge?sslmode=disable"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastRunID, err := rQueue.GetServerRunID(ctx)
	if err != nil {
		log.Printf("Warning: could not fetch initial Redis run_id: %v", err)
	} else {
		log.Printf("Sweeper started. Tracking Redis run_id=%s", lastRunID)
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down sweeper cleanly.")
			return
		case <-ticker.C:
			runSweepCycle(ctx, pgStore, rQueue, &lastRunID)
		}
	}
}

func runSweepCycle(
	ctx context.Context,
	pgStore *store.Store,
	rQueue *queue.RedisClient,
	lastRunID *string,
) {
	recoverPendingQueue(ctx, pgStore, rQueue)
	checkRedisRestart(ctx, pgStore, rQueue, lastRunID)
	recoverDeadWorkers(ctx, pgStore, rQueue)
}

func recoverPendingQueue(ctx context.Context, pgStore *store.Store, rQueue *queue.RedisClient) {
	stale, err := pgStore.GetStalePendingSubmissions(ctx, 5*time.Second)
	if err != nil {
		log.Printf("Sweeper error querying PENDING_QUEUE: %v", err)
		return
	}

	for i := range stale {
		sub := &stale[i]
		log.Printf("[Sweeper] Recovering stuck PENDING_QUEUE submission %s", sub.SubmissionID)
		if err := rQueue.PushSubmission(ctx, sub); err != nil {
			log.Printf("[Sweeper] Failed to push %s to Redis: %v", sub.SubmissionID, err)
			continue
		}
		if err := pgStore.UpdateStatus(ctx, sub.SubmissionID, models.StatusQueued); err != nil {
			log.Printf("[Sweeper] Failed to update status to QUEUED for %s: %v", sub.SubmissionID, err)
		}
	}
}

func checkRedisRestart(
	ctx context.Context,
	pgStore *store.Store,
	rQueue *queue.RedisClient,
	lastRunID *string,
) {
	currentRunID, err := rQueue.GetServerRunID(ctx)
	if err != nil || currentRunID == "" {
		return
	}

	if *lastRunID != "" && currentRunID != *lastRunID {
		log.Printf("[Sweeper] Redis restart detected! Old run_id=%s, New run_id=%s. Re-syncing QUEUED submissions...", *lastRunID, currentRunID)
		queued, err := pgStore.GetAllQueuedSubmissions(ctx)
		if err != nil {
			log.Printf("[Sweeper] Failed to fetch QUEUED submissions: %v", err)
			return
		}
		for i := range queued {
			sub := &queued[i]
			if err := rQueue.PushSubmission(ctx, sub); err != nil {
				log.Printf("[Sweeper] Failed to re-push %s: %v", sub.SubmissionID, err)
			}
		}
	}
	*lastRunID = currentRunID
}

func recoverDeadWorkers(ctx context.Context, pgStore *store.Store, rQueue *queue.RedisClient) {
	processing, err := pgStore.GetProcessingSubmissions(ctx)
	if err != nil {
		log.Printf("Sweeper error querying PROCESSING submissions: %v", err)
		return
	}

	now := time.Now().UTC()

	for i := range processing {
		sub := &processing[i]

		stepLimit := 10 * time.Second
		testLimit := time.Duration(sub.TimeLimitMS) * time.Millisecond
		if testLimit > stepLimit {
			stepLimit = testLimit
		}
		maxSilence := stepLimit + 5*time.Second

		lastSeen := sub.SubmittedAt
		if sub.StartedAt != nil {
			lastSeen = *sub.StartedAt
		}

		live, err := rQueue.GetLiveStatus(ctx, sub.SubmissionID)
		if err == nil && live != nil && !live.LastHeartbeat.IsZero() {
			lastSeen = live.LastHeartbeat
		}

		if now.Sub(lastSeen) <= maxSilence {
			continue
		}

		if sub.RetryCount+1 >= MaxRetries {
			log.Printf("[Sweeper] Submission %s exceeded max retries (%d). Marking INTERNAL_ERROR (DLQ).", sub.SubmissionID, MaxRetries)
			msg := "Exceeded maximum retry attempts (3) due to repeated worker failures"
			_ = pgStore.SaveFinalVerdict(ctx, sub.SubmissionID, models.StatusInternalError, &msg, nil, nil)
			continue
		}

		newCount, err := pgStore.IncrementRetryAndRequeue(ctx, sub.SubmissionID)
		if err != nil {
			log.Printf("[Sweeper] Failed to increment retry for %s: %v", sub.SubmissionID, err)
			continue
		}

		sub.RetryCount = newCount
		sub.Status = models.StatusQueued

		if err := rQueue.PushSubmission(ctx, sub); err != nil {
			log.Printf("[Sweeper] Failed to re-queue dead submission %s: %v", sub.SubmissionID, err)
			continue
		}

		log.Printf("[Sweeper] Recovered dead worker job %s (retry %d/%d) -> Re-queued!", sub.SubmissionID, newCount, MaxRetries)
	}
}