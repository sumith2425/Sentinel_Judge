# SentinelJudge — System Architecture & Engineering Decision Records (ADR)

SentinelJudge is a distributed, fault-tolerant Remote Code Execution (RCE) and automated grading engine designed to evaluate untrusted source code under strict kernel-level resource constraints (`cgroups`, namespaces, network isolation) while surviving bursty traffic spikes and node failures on Kubernetes.

---

## 1. High-Level System Workflow (End-to-End Lifecycle)

```text
[Client]
   │
   │ 1. POST /submit (code, lang, limits, question_id, user_id)
   ▼
[Stateless Go API Pod] ──(2. Check Rate Limit: INCR/EXPIRE)──► [Redis (RAM)]
   │
   ├── 3. INSERT submission (status = PENDING_QUEUE) ────────► [PostgreSQL (Disk Ledger)]
   │
   ├── 4. LPUSH full job payload + HSET initial status ──────► [Redis Queue & Status Hash]
   │
   ├── 5. UPDATE status = QUEUED ────────────────────────────► [PostgreSQL (Disk Ledger)]
   │
   └── 6. Return HTTP 202 Accepted (< 50ms) with submission_id
                                                                      │
   ┌──────────────────────────────────────────────────────────────────┘
   │ 7. Blocking Pop (BRPOP queue:high queue:normal)
   ▼
[Isolated Worker Pod] (Processes strictly 1 job at a time)
   │
   ├── 8. Mark status = PROCESSING, started_at = NOW() ──────► [PostgreSQL & Redis]
   │
   ├── 9. Spawn Ephemeral Sandbox Container (No Net, Cgroup RAM/CPU limits)
   │      ├── Compile Source Code
   │      │     └── Update Redis Hash + Pub/Sub (status = COMPILING, heartbeat = NOW())
   │      └── Run Test Cases (1..N)
   │            └── After each test k: Update Redis Hash + Pub/Sub (status = RUNNING k/N, heartbeat = NOW())
   │
   └── 10. Persist Final Verdict (AC / TLE / MLE / RTE / CE) ─► [PostgreSQL & Redis Pub/Sub]
```

---

## 2. Core Data Contracts & Trust Boundaries

### 2.1 Why We Excluded Full User CRUD
SentinelJudge is architected as a decoupled execution backend (similar to Judge0 or the internal grading cluster of LeetCode/Codeforces). Authentication and problem authoring belong to upstream client services. We accept `user_id` strictly for rate-limiting and auditing, keeping the engine focused on high-throughput scheduling and sandbox isolation.

### 2.2 Client vs. Server-Controlled Fields
To prevent contest-time spoofing or state manipulation, fields are strictly partitioned:

* **Client-Supplied (`SubmissionRequest`):**
  * `user_id` (String / UUID)
  * `question_id` (String)
  * `language` (`cpp`, `python`, `go`)
  * `code` (Raw source string, hard-capped at `64 KB` at the API gateway)
  * `time_limit_ms` (Integer, e.g., `2000`)
  * `memory_limit_mb` (Integer, e.g., `256`)

* **Backend-Generated & Managed (`Submission`):**
  * `submission_id` (UUID v4, generated upon arrival)
  * `submitted_at` (Server UTC timestamp — **never** trust client clocks to prevent deadline spoofing)
  * `started_at` & `completed_at` (Server UTC timestamps)
  * `status` (State machine enum)
  * `verdict_details` (Compiler stderr or failing test case index)
  * `execution_time_ms` & `execution_memory_kb` (Measured from sandbox cgroup metrics)
  * `retry_count` (Integer, initialized to `0`, used for poison-pill detection)

---

## 3. Submission State Machine

```text
                  ┌─────────────────┐
                  │  PENDING_QUEUE  │ (Written to PostgreSQL before Redis push)
                  └────────┬────────┘
                           │ (Redis LPUSH confirmed)
                           ▼
                  ┌─────────────────┐
             ┌───►│     QUEUED      │◄───────────────────┐
             │    └────────┬────────┘                    │
             │             │ (Worker BRPOP)              │
             │             ▼                             │
             │    ┌─────────────────┐                    │
             │    │   PROCESSING    │                    │
             │    │  ├─ COMPILING   │                    │
(Sweeper     │    │  └─ RUNNING k/N │                    │ (Sweeper re-queues
 re-queues   │    └────────┬────────┘                    │  if Worker Heartbeat
 if Redis    │             │                             │  expires & retries < 3)
 lost queue) │             ├─────────────────────────────┘
             │             │
             │             ├──► [If crash & retry_count >= 3] ──► INTERNAL_ERROR (DLQ)
             │             │
             │             ▼ (Execution Finishes Deterministically)
             │    ┌──────────────────────────────────────────────────┐
             └────┤ Final Verdicts:                                  │
                  │ ACCEPTED | TLE | MLE | RUNTIME_ERROR | COMP_ERR  │
                  └──────────────────────────────────────────────────┘
```

---

## 4. Architecture Decision Records (Trade-Offs & Math)

### ADR-01: Monorepo Structure in Go
* **Decision:** Maintain the API Server (`/cmd/api`), Execution Worker (`/cmd/worker`), Recovery Sweeper, and shared packages (`/internal/models`, `/internal/queue`, `/internal/sandbox`) inside a single Go module.
* **Why:** Go compiles into small, statically linked binaries with minimal memory overhead and high concurrency. A monorepo guarantees that the `Submission` struct, Redis key schemas, and state constants never drift between the producer (API) and consumer (Worker).

---

### ADR-02: Why External Redis Queue Instead of In-Memory Go Channels/Queues
* **Decision:** Use Redis Lists/Streams as an external message broker between API pods and Worker pods.
* **Why In-Memory Queues Fail:**
  1. **Container Memory Isolation:** In Kubernetes, API pods and Worker pods run in separate Linux memory namespaces (and potentially different nodes). A Worker process cannot read a Go channel or slice residing in an API container's RAM.
  2. **Horizontal Scaling Silos:** Scaling the API to 3 replicas with in-memory queues creates 3 isolated queues, requiring custom peer-to-peer routing for workers to discover jobs.
  3. **Process Volatility:** If an API container restarts or crashes during a 2,000-submission contest spike, an in-memory queue wipes all 2,000 submissions instantly.

---

### ADR-03: Write-Ahead Ledger Pattern (PostgreSQL First, Redis Second)
* **Decision:** On `POST /submit`, the API writes the submission to PostgreSQL (`status = 'PENDING_QUEUE'`) **before** pushing the job into the Redis queue (`LPUSH`), then marks it `QUEUED`.
* **Why:** Redis is an in-memory store optimized for speed, whereas PostgreSQL provides ACID disk durability. Writing to PostgreSQL first ensures a permanent ledger record exists before acknowledging the user request.
* **Edge Case Handled (The Dual-Write Failure):** If the PostgreSQL `INSERT` succeeds, but the network call to Redis `LPUSH` fails or times out on the next line of code, the submission remains in `PENDING_QUEUE`. A background recovery sweeper queries for rows in `PENDING_QUEUE` older than 5 seconds and pushes them into Redis, guaranteeing zero lost submissions.

---

### ADR-04: Full JSON Payload in Redis Queue vs. ID-Only vs. Pre-Fetcher Worker
* **Context:** We evaluated three ways to structure the Redis queue payload:
  * *Option A (Full Payload):* Push the serialized JSON submission (metadata + source code) directly into the Redis queue.
  * *Option B (ID-Only):* Push only the 36-byte `submission_id` UUID into Redis; Workers run a `SELECT` query against PostgreSQL after popping.
  * *Option C (Two-Stage Pre-Fetcher):* Store IDs in a primary queue and use an intermediary worker to hydrate the top $K$ items from PostgreSQL into a secondary queue.
* **Back-of-the-Envelope Math (Simulating a 10,000-Submission Spike):**
  * Average source code size: `~4 KB` (Hard API limit: `64 KB`).
  * **Memory in Redis for 10,000 queued items:**
    * *Option B (ID-only):* $10,000 \times 36\text{ B} \approx 360\text{ KB}$.
    * *Option A (Full Payload — Average):* $10,000 \times 4.2\text{ KB} \approx 42\text{ MB}$ (`< 0.3%` of a 16 GB node).
    * *Option A (Full Payload — Worst-Case 64 KB flood):* $10,000 \times 64.2\text{ KB} \approx 642\text{ MB}$.
  * **Latency Comparison per Submission:**
    * Redis `BRPOP` difference (36 B vs. 4 KB): `~0.2 ms`.
    * PostgreSQL Primary Key lookup: `~1.5 ms` vs. Code Compilation & Execution: `500 ms – 2,000 ms` (`0.1%` of worker time).
* **Decision:** **Option A (Full JSON Payload in Redis Queue).**
* **Why:** 42 MB of transient Redis RAM is negligible and is freed as soon as items are popped. Crucially, Option A eliminates **100% of worker `SELECT` queries against PostgreSQL** during traffic spikes. When 1,000 users submit simultaneously, PostgreSQL is already absorbing heavy `INSERT` load from API pods; keeping workers off PostgreSQL until the final verdict avoids connection pool exhaustion (`max_connections`) and disk I/O contention. Option C (Pre-fetcher) was rejected because it doubles Redis network round-trips, does not reduce SQL read load, and introduces a second worker crash surface to monitor.

---

### ADR-05: Priority Queueing via Multi-List Blocking Pop
* **Decision:** Support multiple logical Redis queues (e.g., `queue:contest_high`, `queue:practice_normal`) consumed via a single atomic command: `BRPOP queue:contest_high queue:practice_normal 0`.
* **Why:** Prevents priority inversion during live contests (where a burst of practice submissions could block contest grading) without needing complex custom scheduler logic. Redis natively checks lists in strict priority order and blocks with `0%` CPU when all queues are empty.

---

### ADR-06: Write-Offloading Intermediate States (Protecting PostgreSQL from MVCC Churn)
* **Problem:** A single submission running 20 test cases generates ~22 state transitions (`Compiling` $\rightarrow$ `Running 1/20` $\rightarrow$ ... $\rightarrow$ `Accepted`). With 50 concurrent workers, writing every transition to PostgreSQL generates ~1,000 `UPDATE` queries/sec. Because PostgreSQL uses MVCC (Multi-Version Concurrency Control), every `UPDATE` writes a full dead tuple on disk, causing severe table bloat and disk thrashing. Furthermore, 1,000 clients polling `GET /status` every 500ms would add 2,000 `SELECT` queries/sec.
* **Decision:** **Store intermediate statuses exclusively in Redis (Hash + Pub/Sub); write to PostgreSQL only on state boundaries (`PENDING_QUEUE` $\rightarrow$ `QUEUED` $\rightarrow$ `PROCESSING` $\rightarrow$ `FINAL_VERDICT`).**
* **Why:**
  1. **Stateless Polling:** When a Worker completes Test Case $k$, it updates a Redis Hash (`HSET status:<sub_id> state "Running k/20"`). Any horizontally scaled API pod can serve `GET /status/:id` from Redis in `< 0.5 ms` with zero PostgreSQL load.
  2. **Real-Time Streaming (Pub/Sub):** Simultaneously, the Worker executes `PUBLISH updates:<sub_id> "Running k/20"`. In Kubernetes, a user's WebSocket/SSE stream might be connected to `API-Pod-2` while `Worker-Pod-5` grades their code. Redis Pub/Sub routes the event to whichever API pod holds the active client connection.

---

### ADR-07: Fault Recovery, Redis Persistence (`AOF everysec`), and Piggybacked Heartbeats
* **1. Redis Durability vs. Throughput (`appendfsync everysec`):**
  * **Problem:** Enabling `appendfsync always` forces Redis to wait for physical SSD flush on every `LPUSH`, dropping throughput by 20x–50x (from ~100k ops/sec to ~2k–5k ops/sec).
  * **Decision:** Configure Redis with `--appendonly yes --appendfsync everysec`.
  * **Why:** Redis writes to RAM instantly and flushes to disk once per second on a background thread, preserving ~80k–100k ops/sec. Even in the worst-case 1-second power-loss window, PostgreSQL holds the durable `QUEUED` ledger. The recovery sweeper detects Redis restarts (via Redis `INFO server` `run_id` changes or stale `QUEUED` timestamps) and re-enqueues any dropped jobs from PostgreSQL.
* **2. Detecting Dead Workers Without False-Positive Timeouts (Piggybacked Heartbeats):**
  * **Problem:** If a Worker pod gets `OOMKilled` or evicted (`SIGTERM`) by Kubernetes mid-execution, the submission stays in `PROCESSING`. However, using a fixed timeout on `started_at` (e.g., 10 seconds) fails: a problem with 10 test cases and a 2.0s limit can legitimately take $1.5\text{s (compile)} + (10 \times 1.5\text{s}) = 16.5\text{ seconds}$ and still pass every test case. A 10s static timeout would falsely re-queue a healthy job.
  * **Decision:** **Piggyback a `last_heartbeat_at` timestamp onto the existing per-test-case Redis status update.**
  * **Why:** Because the Worker *already* updates Redis after compilation and after every single test case (`1/10`, `2/10`, ...), setting `last_heartbeat_at = NOW()` in that same `HSET` command adds **zero extra network calls**. The recovery sweeper checks if `NOW() - last_heartbeat_at > max(compile_limit, test_case_time_limit) + 3s_buffer`. If exceeded, the Worker is deterministically dead.
* **3. Poison-Pill Protection (Dead-Letter Queue via `retry_count`):**
  * **Problem:** If a malicious or malformed submission triggers a fatal crash in the Worker binary itself, re-queueing it indefinitely would crash Worker 1, then Worker 2, then Worker 3 in an infinite loop.
  * **Decision:** Maintain a `retry_count` column in PostgreSQL. Every time the sweeper recovers a dead `PROCESSING` job, it atomically increments `retry_count`. Once `retry_count >= 3`, the job is permanently transitioned to `INTERNAL_ERROR` (DLQ) and removed from rotation.

---

### ADR-08: Single-Tenant Worker Execution (1 Job per Sandbox) vs. Multi-Threaded Worker
* **Context:** We compared running 1 submission at a time per Worker/Sandbox container (**Design 1**) vs. having a single Worker container spawn multiple goroutines to compile and execute $N$ submissions concurrently in the same container (**Design 2**).
* **Decision:** **Design 1 — Strictly 1 active submission per isolated sandbox environment.**
* **Why Design 2 Fails in an RCE Engine:**
  1. **Cgroup Resource Contention & Collateral OOM Kills:** Linux `cgroups` enforce memory limits (e.g., `256 MB`) at the container boundary, not per Go routine. If Submission A allocates 256 MB in a shared container, the Linux kernel OOM-kills the entire container—terminating innocent Submissions B, C, and D running alongside it, corrupting their verdicts, and falsely incrementing their `retry_count` (the "Domino Effect").
  2. **Noisy-Neighbor CPU Starvation:** If Submission A runs an infinite `while(1)` loop, it saturates the shared CPU quota, starving Submissions B and C and causing them to falsely fail with `Time Limit Exceeded (TLE)`.
  3. **Filesystem Collisions:** Concurrent compilations (`g++ main.cpp -o main`) in a shared workspace overwrite binaries and test input pipes unless isolated via separate mount namespaces.