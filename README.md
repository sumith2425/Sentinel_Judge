# SentinelJudge

**A Distributed, Sandboxed Remote Code Execution (RCE) & Load-Testing Engine built with Go, Redis, PostgreSQL, Docker, and Kubernetes.**

SentinelJudge is an automated grading and code execution backend (modeled after the core execution infrastructure of LeetCode, Codeforces, and Judge0). It accepts untrusted source code (`C++`, `Python`, `Go`) during high-concurrency traffic spikes, isolates execution inside kernel-constrained containers (`cgroups`, namespaces, read-only filesystems, zero-trust networking), and streams real-time state transitions back to clients with deterministic fault recovery.

> **Detailed System Design & Trade-Offs:** See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the complete Architecture Decision Records (ADRs), back-of-the-envelope capacity math, state machines, and failure recovery protocols.

---

## 1. Core Engineering Highlights

* **Write-Ahead Durable Queueing:** Combines PostgreSQL (ACID ledger) with Redis Lists (`AOF everysec`) to guarantee `< 50ms` `202 Accepted` ingestion latency without risking submission loss during broker restarts or dual-write failures.
* **Zero-SQL-Read Worker Pipeline:** Pushes hydrated JSON job payloads (~4 KB) directly into priority Redis queues (`BRPOP queue:high queue:normal`), eliminating 100% of worker `SELECT` queries against PostgreSQL during contest traffic spikes.
* **MVCC-Safe Live Telemetry:** Offloads high-frequency intermediate state updates (`Compiling` -> `Running Test k/N`) to Redis Hashes and Redis Pub/Sub (Server-Sent Events / WebSockets), writing to PostgreSQL strictly on state boundaries.
* **Piggybacked Heartbeats & Poison-Pill Protection:** Workers update `last_heartbeat_at` timestamps automatically alongside per-test-case status updates. A background recovery sweeper detects dead/OOM-killed workers without false-positive static timeouts and routes repeatedly crashing jobs (`retry_count >= 3`) to an `INTERNAL_ERROR` Dead-Letter state.
* **Single-Tenant Kernel Sandboxing:** Enforces strictly 1 active job per ephemeral sandbox container with hard memory ceilings (`OOMKilled` Exit Code 137 -> `MLE`), CPU quotas (`TLE`), PID limits (fork-bomb defense), and `--network=none`.

---

## 2. Repository Structure

```text
sentinel-judge/
├── cmd/
│   ├── api/                # HTTP Ingestion, Rate Limiter, Status Polling & SSE Streamer
│   ├── worker/             # Queue Consumer, Sandbox Orchestrator & Grading Engine
│   └── sweeper/            # Fault-Recovery Daemon (Orphaned Queue & Dead-Worker Sweeper)
├── internal/
│   ├── config/             # Environment variable loading & runtime configuration
│   ├── models/             # Shared data contracts (Submission, LiveStatusUpdate, Enums)
│   ├── store/              # PostgreSQL repository layer (Write-ahead ledger & final verdicts)
│   ├── queue/              # Redis client (Rate limiting, Priority Queue, Status Hash, Pub/Sub)
│   └── sandbox/            # Ephemeral Docker / Linux container runner & resource inspector
├── migrations/
│   └── 001_init.sql        # PostgreSQL schema and composite indexes
├── testdata/
│   └── problems/           # Sample problem test-case bundles (inputs & expected outputs)
├── deploy/
│   ├── docker/             # Multi-stage Dockerfiles for API, Worker, Sweeper, and Sandbox images
│   └── k8s/                # Kubernetes manifests (Deployments, StatefulSets, NetworkPolicies, HPA)
├── docs/
│   └── ARCHITECTURE.md     # Full System Design & ADR documentation
├── docker-compose.yml      # Local development stack (PostgreSQL + Redis with AOF)
└── go.mod
```

---

## 3. Implementation Roadmap (Step-by-Step Milestones)

*Note for AI Assistants: We build this system strictly one milestone at a time. Never implement a future milestone until the current milestone is verified and committed.*

### Phase 1: Core Data Contracts, Storage & Ingestion Pipeline
- [ ] **Milestone 1: Infrastructure & Shared Contracts**
  - Initialize Go module (`github.com/sumith2425/sentinel-judge`) and folder layout.
  - Configure `docker-compose.yml` for PostgreSQL 16 and Redis 7 (`--appendonly yes --appendfsync everysec`).
  - Write `migrations/001_init.sql` with `submissions` table and `(status, submitted_at)` composite index.
  - Define core structs and state constants in `internal/models/submission.go`.
- [ ] **Milestone 2: Storage & Queue Repositories**
  - Implement PostgreSQL data access layer (`internal/store/postgres.go`) with connection pooling.
  - Implement Redis client (`internal/queue/redis.go`) for IP/User sliding/fixed-window rate limiting (`INCR`/`EXPIRE`), priority queue push/pop (`LPUSH`/`BRPOP`), and live status Hash/PubSub updates.
- [ ] **Milestone 3: Stateless Ingestion & Live Status API (`cmd/api`)**
  - Build `POST /api/v1/submit`: payload validation (64 KB code cap), Redis rate-limit check, Postgres `PENDING_QUEUE` insert, Redis queue push + initial status hash set, Postgres `QUEUED` update, return `202 Accepted` under 50ms.
  - Build `GET /api/v1/submissions/:id`: fast read from Redis status hash with fallback to PostgreSQL for completed historical submissions.
  - Build `GET /api/v1/submissions/:id/stream`: Server-Sent Events (SSE) endpoint subscribing to Redis Pub/Sub for real-time state transitions.

### Phase 2: Sandboxed Execution Worker & Fault Recovery
- [ ] **Milestone 4: Ephemeral Docker Sandbox Runner (`internal/sandbox`)**
  - Create minimal toolchain images for `cpp` (`g++`), `python` (`python3`), and `go`.
  - Implement sandbox execution controller: mount source code and read-only test cases, enforce `--network=none`, `--memory`, `--memory-swap`, `--cpus`, `--pids-limit`, and read-only root filesystem.
  - Inspect container exit codes (`137` / `OOMKilled` -> `MEMORY_LIMIT_EXCEEDED`, context timeout -> `TIME_LIMIT_EXCEEDED`, non-zero exit -> `RUNTIME_ERROR`) and compare stdout against expected outputs.
- [ ] **Milestone 5: Worker Consumer Loop (`cmd/worker`)**
  - Pull hydrated jobs via `BRPOP`, transition state to `PROCESSING`, execute compilation and test cases sequentially, emit Redis Hash + Pub/Sub updates (`last_heartbeat_at`) per test case, and persist final verdict to PostgreSQL.
  - Handle `SIGTERM` / `SIGINT` for graceful shutdown (finish active submission before exiting).
- [ ] **Milestone 6: Fault Recovery Sweeper (`cmd/sweeper`)**
  - Detect and re-queue `PENDING_QUEUE` submissions older than 5 seconds (dual-write recovery).
  - Detect Redis restarts via `run_id` changes and recover unqueued items.
  - Detect dead workers via expired `last_heartbeat_at` timestamps, increment `retry_count`, and re-queue or move to `INTERNAL_ERROR` when `retry_count >= 3`.

### Phase 3: Containerization, Kubernetes Orchestration & Load Testing
- [ ] **Milestone 7: Multi-Stage Dockerfiles & Local Compose Integration**
  - Build minimal production Docker images for `api`, `worker`, and `sweeper`.
- [ ] **Milestone 8: Multi-Node Kubernetes Deployment (`kind` / `k3s`)**
  - Write Kubernetes manifests: `StatefulSet` (Postgres, Redis), `Deployment` (API, Worker, Sweeper), `PersistentVolumeClaim` (test cases), and `Ingress`.
  - Enforce Zero-Trust `NetworkPolicy` (isolate sandbox/worker pods from internal cluster services and internet).
  - Configure Horizontal Pod Autoscaler (HPA) and `terminationGracePeriodSeconds` for safe worker scale-down.
- [ ] **Milestone 9: Chaos & Load Testing**
  - Simulate a 1,000-submission contest spike (`k6` / custom Go load generator).
  - Verify resource enforcement against fork bombs, memory bombs, infinite loops, and `kubectl delete pod` chaos mid-execution.

---

## 4. Development & AI Collaboration Rules

1. **Architect-Led Workflow:** All architectural decisions follow [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
2. **Code Standards:**
   - Do **not** include comments in any generated source code files.
   - Keep packages decoupled (`cmd/*` wires dependencies; business logic lives in `internal/*`).
   - Handle all errors explicitly; never ignore returned errors (`_`).
3. **Incremental Verification:** Every milestone must be verified via `docker compose`, `psql`, `redis-cli`, or integration tests before committing to Git.