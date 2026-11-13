package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sumith2425/sentinel-judge/internal/models"
	"github.com/sumith2425/sentinel-judge/internal/queue"
	"github.com/sumith2425/sentinel-judge/internal/store"
)

type Server struct {
	store *store.Store
	queue *queue.RedisClient
	rdb   *redis.Client
}

func main() {
	pgDSN := os.Getenv("POSTGRES_DSN")
	if pgDSN == "" {
		pgDSN = "postgres://sentinel:sentinel_pass@localhost:5432/sentinel_judge?sslmode=disable"
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "./web"
	}

	pgStore, err := store.NewPostgresStore(pgDSN)
	if err != nil {
		log.Fatalf("Failed to initialize postgres store: %v", err)
	}
	defer pgStore.Close()

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := pgStore.EnsureProblemSchema(initCtx); err != nil {
		log.Fatalf("Failed to ensure problem schema: %v", err)
	}
	initCancel()

	rQueue, err := queue.NewRedisClient(redisAddr, "", 0)
	if err != nil {
		log.Fatalf("Failed to initialize redis client: %v", err)
	}
	defer rQueue.Close()

	rawRDB := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer rawRDB.Close()

	srv := &Server{
		store: pgStore,
		queue: rQueue,
		rdb:   rawRDB,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/api/v1/health", srv.handleHealth)
	r.Get("/api/v1/problems", srv.handleListProblems)
	r.Get("/api/v1/problems/{id}", srv.handleGetProblem)
	r.Post("/api/v1/problems", srv.handleUpsertProblem)
	r.Post("/api/v1/submit", srv.handleSubmit)
	r.Get("/api/v1/submissions/{id}", srv.handleGetStatus)
	r.Get("/api/v1/submissions/{id}/stream", srv.handleStreamStatus)

	fileServer := http.FileServer(http.Dir(webDir))
	r.Handle("/*", fileServer)

	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		log.Println("API & Web Workspace listening on http://localhost:8080")
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down API server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("Forced shutdown error: %v", err)
	}
	log.Println("API server stopped")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "OPERATIONAL",
	})
}

func (s *Server) handleListProblems(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProblems(r.Context())
	if err != nil {
		http.Error(w, "Failed to retrieve problem catalog", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

func (s *Server) handleGetProblem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	problem, err := s.store.GetProblem(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to query problem", http.StatusInternalServerError)
		return
	}
	if problem == nil {
		http.Error(w, "Problem not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(problem)
}

func (s *Server) handleUpsertProblem(w http.ResponseWriter, r *http.Request) {
	var p models.Problem
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid problem JSON payload", http.StatusBadRequest)
		return
	}
	if p.ID == "" || p.Title == "" {
		http.Error(w, "Problem id and title are required", http.StatusBadRequest)
		return
	}
	if err := s.store.UpsertProblem(r.Context(), &p); err != nil {
		http.Error(w, fmt.Sprintf("Failed to persist problem: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     p.ID,
		"status": "UPSERTED",
	})
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req models.SubmissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if len(req.Code) == 0 {
		http.Error(w, "Source code cannot be empty", http.StatusBadRequest)
		return
	}

	if len(req.Code) > 64*1024 {
		http.Error(w, "Code exceeds maximum size limit of 64 KB", http.StatusBadRequest)
		return
	}

	if req.TimeLimitMS <= 0 || req.TimeLimitMS > 10000 {
		req.TimeLimitMS = 2000
	}
	if req.MemoryLimitMB <= 0 || req.MemoryLimitMB > 1024 {
		req.MemoryLimitMB = 512
	}

	rateLimitKey := fmt.Sprintf("ratelimit:%s", req.UserID)
	allowed, err := s.queue.AllowRateLimit(r.Context(), rateLimitKey, 15, 1*time.Minute)
	if err != nil {
		http.Error(w, "Rate limit error", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Rate limit exceeded (maximum 15 submissions per minute)", http.StatusTooManyRequests)
		return
	}

	subID := uuid.New()
	sub := &models.Submission{
		SubmissionID:  subID,
		UserID:        req.UserID,
		QuestionID:    req.QuestionID,
		Language:      req.Language,
		Code:          req.Code,
		TimeLimitMS:   req.TimeLimitMS,
		MemoryLimitMB: req.MemoryLimitMB,
		Status:        models.StatusPendingQueue,
		RetryCount:    0,
		SubmittedAt:   time.Now().UTC(),
	}

	if err := s.store.CreateSubmission(r.Context(), sub); err != nil {
		http.Error(w, "Failed to persist submission", http.StatusInternalServerError)
		return
	}

	if err := s.queue.PushSubmission(r.Context(), sub); err != nil {
		http.Error(w, "Failed to enqueue submission", http.StatusInternalServerError)
		return
	}

	if err := s.store.UpdateStatus(r.Context(), subID, models.StatusQueued); err != nil {
		log.Printf("Failed to update status to QUEUED for %s: %v", subID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"submission_id": subID,
		"status":        models.StatusQueued,
	})
}

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	subID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid submission UUID", http.StatusBadRequest)
		return
	}

	sub, err := s.store.GetSubmission(r.Context(), subID)
	if err == nil && sub != nil && models.IsTerminalStatus(sub.Status) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sub)
		return
	}

	liveUpdate, err := s.queue.GetLiveStatus(r.Context(), subID)
	if err == nil && liveUpdate != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(liveUpdate)
		return
	}

	if sub == nil {
		http.Error(w, "Submission not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sub)
}

func (s *Server) handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	subID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "Invalid submission UUID", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	channel := fmt.Sprintf("%s%s", queue.ChannelKeyPrefix, subID.String())
	pubsub := s.rdb.Subscribe(r.Context(), channel)
	defer pubsub.Close()

	ch := pubsub.Channel()

	if live, err := s.queue.GetLiveStatus(r.Context(), subID); err == nil && live != nil {
		if b, err := json.Marshal(live); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", string(b))
			flusher.Flush()
			if models.IsTerminalStatus(live.Status) {
				return
			}
		}
	} else if sub, err := s.store.GetSubmission(r.Context(), subID); err == nil && sub != nil {
		initEvent := models.LiveStatusUpdate{
			SubmissionID:      sub.SubmissionID,
			Status:            sub.Status,
			VerdictDetails:    sub.VerdictDetails,
			ExecutionTimeMS:   sub.ExecutionTimeMS,
			ExecutionMemoryKB: sub.ExecutionMemoryKB,
			LastHeartbeat:     time.Now().UTC(),
		}
		if b, err := json.Marshal(initEvent); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", string(b))
			flusher.Flush()
			if models.IsTerminalStatus(sub.Status) {
				return
			}
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			flusher.Flush()

			var parsed models.LiveStatusUpdate
			if err := json.Unmarshal([]byte(msg.Payload), &parsed); err == nil {
				if models.IsTerminalStatus(parsed.Status) {
					return
				}
			}
		}
	}
}