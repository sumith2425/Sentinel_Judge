# SentinelJudge

A distributed, fault-tolerant remote code execution and automated grading engine engineered in **Go**, **PostgreSQL**, **Redis**, **Docker**, and **Kubernetes**.

SentinelJudge decouples high-throughput submission ingestion from heavy, untrusted code execution using a write-ahead queueing pattern and strict Linux kernel sandboxing (`cgroups` + `namespaces`).

---

## System Architecture

```
+----------+      HTTP POST / SSE       +-------------------+
|  Client  | -------------------------> |   Stateless API   |
+----------+                            +-------------------+
                                           |             |
                      1. Write-Ahead (SQL) |             | 2. Enqueue (LPUSH) /
                                           v             v    PubSub Stream
                                    +------------+  +---------+
                                    | PostgreSQL |  |  Redis  |
                                    +------------+  +---------+
                                           ^             ^
                      4. Final Verdict     |             | 3. Consume (BRPOP) &
                         & Sweeper Sync    |             |    Heartbeat (HSET)
                                        +-------------------+
                                        |   Worker Nodes    |
                                        +-------------------+
                                                  |
                                                  | Spawns Ephemeral Sandbox
                                                  v
                                     +-------------------------+
                                     | Docker Sandbox (Cgroup) |
                                     | --network=none          |
                                     | --memory / --cpus       |
                                     | --pids-limit=64         |
                                     +-------------------------+
```

### Core Engineering Highlights

1. **Write-Ahead Ingestion Pattern (`PENDING_QUEUE` -> `QUEUED`):**
   Every submission is durably persisted to PostgreSQL before entering the Redis queue. If the API crashes mid-flight between PostgreSQL and Redis, the background **Fault-Recovery Sweeper** detects stale `PENDING_QUEUE` records (>5s) and automatically enqueues them.
2. **Zero-Trust Ephemeral Sandboxing:**
   Untrusted C++, Python, and Go programs are compiled in an isolated workspace and executed across test cases with a **read-only root/workspace mount (`:ro`)**, no network interface (`--network=none`), process limits (`--pids-limit=64` to block fork bombs), CPU quotas (`--cpus=1.0`), and hard memory ceilings (`--memory-swap` locked to `--memory` for deterministic Linux OOM Killer exit code `137` detection).
3. **Self-Healing Worker Pool & Poison-Pill Protection:**
   Workers emit real-time test-case progress heartbeats to Redis Hashes and Pub/Sub channels. If a worker node crashes mid-execution, the Sweeper detects expired heartbeats, increments `retry_count`, and re-queues the job. Submissions crashing workers 3 consecutive times are quarantined as `INTERNAL_ERROR` (Dead-Letter Queue behavior).
4. **Real-Time Telemetry via Server-Sent Events (SSE):**
   Clients subscribe to `/api/v1/submissions/{id}/stream` to receive sub-millisecond live updates (`COMPILING`, `RUNNING 2/3`, `ACCEPTED`) pushed via Redis Pub/Sub without polling PostgreSQL.

---

## Verified Benchmark Performance

Tested locally on Ubuntu 24.04 LTS (12th Gen Intel Core i5, 16 GB RAM, NVMe SSD) with 3 parallel Worker containers grading C++ submissions (1 compile container + 3 test-case containers per submission = 80 ephemeral containers total):

| Metric | Measured Result |
| :--- | :--- |
| **Concurrent Burst Size** | 20 simultaneous submissions |
| **Success Rate** | 100% (`20 / 20 ACCEPTED`) |
| **Total Ingestion Window** | `38 ms` |
| **Ingestion Latency (p50)** | `35 ms` |
| **Ingestion Latency (p95 / p99)** | `38 ms` |
| **End-to-End Grading Time (80 Containers)** | `8.22 s` (`2.43 submissions/sec`) |

---

## Repository Structure

```text
.
├── cmd/
│   ├── api/            # Stateless HTTP Ingestion & SSE Streaming Server
│   ├── worker/         # Redis Queue Consumer & Sandbox Orchestrator
│   ├── sweeper/        # Fault-Recovery & Dead-Worker Re-Queue Daemon
│   └── loadtest/       # Concurrent Burst Stress-Testing Suite
├── internal/
│   ├── models/         # Strongly-typed state machine & data contracts
│   ├── store/          # PostgreSQL connection pool & ACID repository
│   ├── queue/          # Redis AOF queue, rate-limiter, and Pub/Sub client
│   └── sandbox/        # Ephemeral Docker container runner with cgroup limits
├── deploy/
│   ├── docker/         # Multi-stage production Dockerfiles (~15MB images)
│   └── k8s/            # Kubernetes Deployments, Services, ConfigMap, and HPA
├── migrations/         # Automated PostgreSQL schema & composite B-Tree index
└── testdata/problems/  # Sample problem test cases (.in / .out)
```

---

## Quick Start

### 1. Pre-pull Sandbox Compiler Images
```bash
docker pull frolvlad/alpine-gxx:latest
docker pull python:3.12-alpine
docker pull golang:1.22-alpine
```

### 2. Launch the Distributed Cluster (with 3 Parallel Workers)
```bash
docker compose up --build -d --scale worker=3
```

### 3. Submit Code for Grading
```bash
curl -i -X POST http://localhost:8080/api/v1/submit \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "demo_user",
    "question_id": "two_sum",
    "language": "cpp",
    "code": "#include <iostream>\nint main() { int a, b; std::cin >> a >> b; std::cout << a + b << std::endl; return 0; }",
    "time_limit_ms": 2000,
    "memory_limit_mb": 128
  }'
```

### 4. Run the Concurrent Load Tester
```bash
go run cmd/loadtest/main.go
```