# SentinelJudge — System Architecture & Engineering Decisions

This document records the core architectural decisions (ADRs), state transitions, and security boundaries of **SentinelJudge**.

---

## 1. State Machine Lifecycle

Every code submission progresses through a strict, deterministic state machine persisted in PostgreSQL and mirrored in Redis during active execution:

```text
                  +-----------------+
                  |  HTTP POST      |
                  +-----------------+
                           |
                           v
                  +-----------------+
                  |  PENDING_QUEUE  |  <-- Persisted in PostgreSQL first (WAL pattern)
                  +-----------------+
                           |
              +------------+------------+
              | (Normal API Push)       | (API Crash > 5s: Sweeper Rescue)
              v                         v
                  +-----------------+
                  |     QUEUED      |  <-- Pushed to Redis List (queue:submissions)
                  +-----------------+
                           |
                           | Worker BRPOP
                           v
                  +-----------------+
                  |   PROCESSING    |  <-- Worker sets started_at = NOW()
                  +-----------------+
                     /           \
    (Worker Alive)  /             \ (Worker Crash / Heartbeat Expired)
                   v               v
        +------------------+     +-----------------------------------+
        | COMPILING        |     | retry_count < 3 ?                 |
        | RUNNING (k / N)  |     |  - Yes: Increment & Re-Queue      |
        +------------------+     |  - No : Quarantine INTERNAL_ERROR |
                 |               +-----------------------------------+
                 v
     +-----------------------+
     |   TERMINAL VERDICTS   |
     | - ACCEPTED            |
     | - WRONG_ANSWER        |
     | - TIME_LIMIT_EXCEEDED |
     | - MEMORY_LIMIT_EXCEED |
     | - COMPILATION_ERROR   |
     | - RUNTIME_ERROR       |
     | - INTERNAL_ERROR      |
     +-----------------------+
```

---

## 2. Architecture Decision Records (ADRs)

### ADR-01: Go Monorepo with Isolated `internal/` Domain Packages
* **Context:** SentinelJudge consists of multiple distinct processes (`api`, `worker`, `sweeper`) sharing identical data contracts, PostgreSQL queries, and Redis keys.
* **Decision:** Use a single Go module (`github.com/sumith2425/sentinel-judge`) with entry points under `cmd/` and shared private packages under `internal/`.
* **Consequence:** Eliminates schema drift between services while compiling into separate, minimal ~15 MB Alpine binaries via multi-stage Dockerfiles.

### ADR-02: Write-Ahead Persistence Before Queueing
* **Context:** Pushing directly to an in-memory queue before writing to disk risks losing submissions if the queue fails, while writing to both without coordination creates dual-write inconsistencies.
* **Decision:** Insert into PostgreSQL with `status = 'PENDING_QUEUE'` first, push to Redis (`LPUSH`), and then update PostgreSQL to `status = 'QUEUED'`.
* **Consequence:** PostgreSQL serves as the single source of truth. A composite B-Tree index on `(status, submitted_at)` enables the Sweeper to find and repair interrupted writes in $O(\log N)$ time.

### ADR-03: Redis AOF (`everysec`) + `run_id` Reboot Detection
* **Context:** Redis holds the active work queue. If the Redis container restarts, in-flight queue items could be lost.
* **Decision:** Configure Redis with Append-Only File (`--appendonly yes --appendfsync everysec`) and monitor Redis `INFO server` `run_id` inside the Sweeper daemon.
* **Consequence:** At most 1 second of queue data is vulnerable to power loss, and if Redis restarts with a new `run_id`, the Sweeper automatically re-synchronizes all `QUEUED` rows from PostgreSQL back into Redis.

### ADR-04: Two-Phase Ephemeral Container Sandboxing (`rw` Compile -> `ro` Run)
* **Context:** Untrusted user code can attempt fork bombs, network exfiltration, disk filling, or reading expected `.out` test files.
* **Decision:**
  1. **Compilation Phase:** Mount only the temporary job directory as Read-Write (`rw`) so the compiler can emit the binary.
  2. **Execution Phase:** Mount the job directory as **Read-Only (`ro`)**, disable networking (`--network=none`), cap PIDs (`--pids-limit=64`), lock memory swap (`--memory-swap == --memory`), and pipe test inputs via `stdin` from the host process.
* **Consequence:** User code never sees expected output files, cannot write to disk during test execution, cannot spawn fork bombs, and triggers a deterministic Linux kernel `OOMKilled` (`Exit Code 137`) the instant allocation exceeds the problem's memory limit.

### ADR-05: Hybrid Telemetry (Redis Hash + Pub/Sub + SSE)
* **Context:** Polling PostgreSQL every 500ms across thousands of clients degrades database IOPS.
* **Decision:** Workers pipeline `SET status:<id>` (2-hour TTL) and `PUBLISH updates:<id>` on every state change. The API streams updates over HTTP Server-Sent Events (`GET /api/v1/submissions/{id}/stream`) and immediately emits the current cached or database state on connection open to avoid race conditions on fast jobs.
* **Consequence:** Zero database polling load during execution and real-time step-by-step progress in the browser.

### ADR-06: Distinguish `WRONG_ANSWER` from `RUNTIME_ERROR`
* **Context:** Program crashes (non-zero exit codes, segfaults) and incorrect algorithmic outputs require separate diagnostic feedback.
* **Decision:** Non-zero container exit codes (excluding `137` OOM) return `RUNTIME_ERROR` with `stderr` output, whereas exit code `0` with mismatched `stdout` returns `WRONG_ANSWER` with expected vs. actual output diffs.

### ADR-07: Same-Origin Embedded Web Workspace
* **Context:** Separate frontend dev servers introduce CORS complexity, large Node.js build dependencies, and multi-container routing overhead.
* **Decision:** Serve a static Vanilla HTML/CSS/JS workspace and problem catalog (`GET /api/v1/problems`) directly from the Go API router (`cmd/api`).
* **Consequence:** Single-command startup (`docker compose up --build -d`), zero CORS configuration, and native `EventSource` SSE streaming.