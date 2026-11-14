# SentinelJudge

## Live Demo

- **Public Instance:** [Sentinel-Judge](https://ratios-becomes-seek-crop.trycloudflare.com)
- **Problemset:** 400 CSES tasks with full hidden test suites

A distributed, fault-tolerant remote code execution (RCE) and automated grading engine engineered in **Go**, **PostgreSQL**, **Redis**, **Docker**, and **Kubernetes**.

SentinelJudge decouples high-throughput submission ingestion from untrusted code execution using a write-ahead queueing pattern, real-time Server-Sent Events (SSE) telemetry, and strict Linux kernel sandboxing (`cgroups` + `namespaces`). It includes a zero-dependency web workspace served directly by the Go API gateway.

---

## System Architecture

```text
+-----------------------------------------------------------------------+
|                        Browser Web Workspace                          |
|     Problem Catalog  |  Monaco Code Editor  |  Live SSE Telemetry     |
+-----------------------------------------------------------------------+
             |                        |                        ^
   GET /api/v1/problems     POST /api/v1/submit      GET /submissions/{id}/stream
             v                        v                        | (SSE Push)
+-----------------------------------------------------------------------+
|                         Stateless HTTP API                            |
+-----------------------------------------------------------------------+
                                |                    |
          1. Write-Ahead (ACID) |                    | 2. Enqueue (LPUSH) &
                                v                    v    Pub/Sub Subscribe
                         +------------+        +-----------+
                         | PostgreSQL |        |   Redis   |
                         +------------+        +-----------+
                                ^                    ^
          4. Final Verdict      |                    | 3. Pop Job (BRPOP) &
             & Sweeper Sync     |                    |    Publish Heartbeat
                         +---------------------------------+
                         |     Worker Pool (N Replicas)    |
                         +---------------------------------+
                                          |
                                          | Spawns Ephemeral Container
                                          v
                         +---------------------------------+
                         |   Docker Sandbox (Linux Kernel) |
                         |   --network=none                |
                         |   --memory / --memory-swap      |
                         |   --cpus=1.0 / --pids-limit=64  |
                         |   Read-Only Workspace (:ro)     |
                         +---------------------------------+
```

For deep-dive Sequence Diagrams and Architecture Decision Records (ADRs 01–09), see **[ARCHITECTURE.md](./ARCHITECTURE.md)**.

---

## Key Engineering Capabilities

1. **Write-Ahead Ingestion (`PENDING_QUEUE` -> `QUEUED`):**
   Every submission is persisted to PostgreSQL before entering Redis. If the API process crashes between the database write and the Redis push, the **Fault-Recovery Sweeper** detects stale `PENDING_QUEUE` rows (>5s) and enqueues them automatically.
2. **Zero-Trust Ephemeral Sandboxing:**
   Untrusted C++, Python, and Go programs compile inside an isolated directory and execute test cases with a **read-only workspace mount (`:ro`)**, disabled networking (`--network=none`), process limits (`--pids-limit=64` to block fork bombs), CPU pinning (`--cpus=1.0`), and locked swap (`--memory-swap` equal to `--memory` for deterministic Linux OOM Killer exit code `137` detection).
3. **Self-Healing Workers & Poison-Pill Protection:**
   Workers broadcast per-test-case heartbeats to Redis Hashes and Pub/Sub channels. If a worker crashes mid-evaluation, the Sweeper detects the expired heartbeat, increments `retry_count`, and re-queues the submission. Submissions that crash workers 3 times are quarantined as `INTERNAL_ERROR`.
4. **Embedded Engineering Workspace:**
   The API binary serves a lightweight, zero-build-step frontend at `http://localhost:8080` featuring problem specifications, multi-language starter templates, one-click adversarial test presets, and a live SSE execution pipeline.

---

## Verified Benchmark Performance

Measured on Ubuntu 24.04 LTS (12th Gen Intel Core i5, 16 GB RAM, NVMe SSD) with 3 parallel Worker containers evaluating C++ submissions (80 ephemeral Docker containers spawned and destroyed across 20 concurrent requests):

| Metric | Measured Value |
| :--- | :--- |
| **Concurrent Burst Size** | 20 simultaneous submissions |
| **Completion Rate** | 100% (`20 / 20 ACCEPTED`) |
| **Total Ingestion Window** | `38 ms` |
| **Ingestion Latency (`p50`)** | `35 ms` |
| **Ingestion Latency (`p95` / `p99`)** | `38 ms` |
| **End-to-End Grading Throughput** | `2.43 submissions/sec` (`8.22 s` wall time) |

---

## Repository Structure

```text
.
├── cmd/
│   ├── api/             # HTTP API Gateway, SSE Streamer & Static Web Server
│   ├── worker/          # Redis Queue Consumer & Sandbox Orchestrator
│   ├── sweeper/         # Fault-Recovery & Dead-Worker Re-Queue Daemon
│   ├── loadtest/        # Concurrent Burst Stress-Testing Suite
│   └── intensive_test/  # Adversarial Security & Edge-Case Verification Suite
├── internal/
│   ├── models/          # State machine, submission structs & problem catalog
│   ├── store/           # PostgreSQL connection pool & ACID queries
│   ├── queue/           # Redis AOF queue, rate-limiter & Pub/Sub telemetry
│   └── sandbox/         # Ephemeral Docker runner with cgroup enforcement
├── web/                 # Precision Engineering Web Workspace (HTML/CSS/JS)
├── deploy/
│   ├── docker/          # Multi-stage production Dockerfiles
│   └── k8s/             # Kubernetes Deployments, Services, ConfigMap & HPA
├── migrations/          # Automated PostgreSQL schema & composite B-Tree index
└── testdata/problems/   # Algorithmic problem bank (.in / .out test suites)
```

---

## Quick Start

### 1. Pre-pull Sandbox Compiler Images
```bash
docker pull frolvlad/alpine-gxx:latest
docker pull python:3.12-alpine
docker pull golang:1.22-alpine
```

### 2. Start the Cluster (3 Parallel Workers)
```bash
docker compose up --build -d --scale worker=3
```

### 3. Open the Web Workspace
Navigate to **[http://localhost:8080](http://localhost:8080)** in your browser to select problems, write or load preset test payloads, and watch live SSE grading telemetry.

### 4. Run Automated Verification Suites
```bash
# Run 20-request concurrent burst benchmark
go run cmd/loadtest/main.go

# Run intensive adversarial & multi-language security suite
go run cmd/intensive_test/main.go
```