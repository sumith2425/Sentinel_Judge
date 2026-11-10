# SentinelJudge

SentinelJudge is a distributed, sandboxed remote code execution (RCE) and automated grading engine designed to run untrusted user code at scale on Kubernetes. 

Modeled after competitive programming platforms (e.g., LeetCode, Codeforces) and continuous integration runners, SentinelJudge accepts source code submissions, evaluates them against multi-case test suites under strict computational constraints, and streams real-time status transitions.

---

## Key Architectural Goals

- **Low-Latency Ingestion:** Non-blocking API layer returning immediate acknowledgments (`202 Accepted`) with sub-50ms ingestion latency.
- **Hardened Sandboxed Execution:** Defense-in-depth isolation using Linux kernel primitives:
  - **Namespaces:** Process, mount, IPC, UTS, and network isolation (`CLONE_NEWNET`).
  - **cgroups v2:** Hard limits on CPU, memory, and process count (`pids.max`) to defeat fork bombs and runaway allocations.
  - **seccomp-bpf:** Syscall whitelisting to block unauthorized system calls.
- **Zero-Trust Network Isolation:** Complete network containment preventing untrusted code from accessing internal cluster services or the external internet.
- **Fault-Tolerant Queue Pipeline:** At-least-once task delivery with visibility leases and dead-letter queues (DLQs) to cleanly handle worker evictions or host failures.
- **Queue-Depth Autoscaling:** Dynamic worker scaling based on queue backlog metrics rather than raw container resource usage.

---

## Architecture Overview

```
                      +-------------------+
                      |   Client / User   |
                      +---------+---------+
                                |
                        HTTP Submission
                                |
                                v
                      +-------------------+
                      |   Ingestion API   | <--- Token Bucket Rate Limiting
                      +---------+---------+
                                |
                        Push to Work Queue
                                |
                                v
                      +-------------------+
                      |   Message Queue   |
                      +---------+---------+
                                |
                        Lease / Consume Job
                                |
                                v
               +---------------------------------+
               |          Worker Pod             |
               |                                 |
               |   +-------------------------+   |
               |   |   Sandbox Runner        |   |
               |   |   (cgroups v2, seccomp, |   |
               |   |    isolated namespaces) |   |
               |   +------------+------------+   |
               |                |                |
               |                v                |
               |   +-------------------------+   |
               |   |  Untrusted User Binary  |   |
               |   +-------------------------+   |
               +---------------------------------+
                                |
                       Publish Results & Verdict
                                |
                                v
                      +-------------------+
                      | Result Pub/Sub DB |
                      +-------------------+
```

---

## Roadmap

- [ ] **Phase 1:** Sandbox Isolation & Linux Kernel Primitives Evaluation
- [ ] **Phase 2:** Zero-Trust Networking & Containment
- [ ] **Phase 3:** Core Sandbox Runner Engine
- [ ] **Phase 4:** Asynchronous Queue Pipeline & Crash Recovery
- [ ] **Phase 5:** Ingestion Gateway & Rate Limiting
- [ ] **Phase 6:** Kubernetes Deployment, Autoscaling & Load Testing
