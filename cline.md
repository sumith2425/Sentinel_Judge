# SentinelJudge: AI Assistant Directives & Context

## 1. Project Overview & Architecture
SentinelJudge is a distributed, sandboxed remote code execution (RCE) and load-testing engine modeled after competitive programming platforms (e.g., LeetCode, Codeforces) and continuous integration runners, architected to run on Kubernetes.

### Core Functional Goals
- **Ingestion & Pre-Queue Filtering:** High-throughput API gateway with token-bucket/leaky-bucket rate limiting, returning `202 Accepted` with a submission ID in under 50ms.
- **Isolated Sandboxed Execution:** Asynchronous worker nodes executing untrusted multi-language user code (e.g., C++, Python, Go) against private test suites.
- **Strict Resource & Security Enforcement:** Kernel-level isolation using cgroups v2, Linux namespaces, and seccomp-bpf. Zero-trust network containment (no internet or cluster-internal reachability for executed code). Deterministic verdict reporting (AC, TLE, MLE, RTE, CE).
- **Resilient Queue Processing:** At-least-once message delivery with explicit leases, visibility timeouts, and dead-letter queues (DLQs) to recover from worker node crashes or pod evictions.
- **Queue-Driven Autoscaling:** Dynamic scaling via queue depth metrics rather than CPU/memory thresholds.

---

## 2. Role & Operating Principles

### Lead Architect Ownership
- The user is the **Lead Architect and system owner**.
- The primary goal of this repository is for the user to deeply learn Docker, Kubernetes, and System Design hands-on.
- The AI acts as a **technical sounding board and implementation partner**.

### Non-Negotiable Operating Rules
1. **No Unsolicited Mass Code Dumps:** Never generate entire microservices or complete end-to-end architectures without explicit direction.
2. **Pedagogical Guidance:** Explain trade-offs, edge cases, failure modes, and low-level Linux/Kubernetes primitives. Ask clarifying design questions to help the user choose the right architectural direction.
3. **Iterative Development:** Propose small, incremental steps.
4. **Code Generation Constraints:**
   - Write code ONLY when specifically instructed and agreed upon.
   - Strictly adhere to a **NO COMMENTS** rule in all generated code.
5. **Git Restrictions:**
   - **NEVER** run `git push`. Pushing to remotes is strictly reserved for the user.
6. **Workspace Boundaries:**
   - Confine all actions, file edits, and commands exclusively to `/mnt/datadrive/Projects/Sentinel_Judge`.

---

## 3. Technology Stack & Primitives Reference
- **Operating System / Kernel:** Linux namespaces (`pid`, `net`, `mnt`, `ipc`, `uts`, `user`), cgroups v2 (`cpu.max`, `memory.max`, `pids.max`), seccomp-bpf syscall filtering.
- **Sandboxing Primitives:** nsjail, bwrap, or custom unshare runners.
- **Orchestration:** Kubernetes (Deployments, Pods, NetworkPolicies, PV/PVC, HPA/KEDA).
- **Messaging & Cache:** Redis / RabbitMQ / NATS JetStream.
- **Language Environments:** C++, Python, Go compilers and runtimes.

---

## 4. Phased Roadmap & Status Tracking

- [ ] **Phase 1: Sandboxing & Execution Isolation Strategy**
  - Explore and compare execution isolation models (K8s Jobs vs. Sibling containers vs. In-pod nsjail/kernel namespaces).
  - Understand Linux kernel primitives: namespaces, cgroups v2, seccomp filters.
- [ ] **Phase 2: Network Security & Zero-Trust Posture**
  - Network namespace isolation (`CLONE_NEWNET` / loopback only).
  - Kubernetes NetworkPolicies for worker pods.
- [ ] **Phase 3: Worker & Sandbox Engine Prototyping**
  - Minimal sandboxed execution harness.
  - Resource limit enforcement (CPU time, wall clock, memory, process limits).
  - Deterministic verdict classification (AC, TLE, MLE, RTE, CE).
- [ ] **Phase 4: Message Queue, Leases & Fault Recovery**
  - Queue selection and consumer group patterns.
  - Heartbeats, lease management, worker crash recovery, DLQ.
- [ ] **Phase 5: Ingestion API & Rate Limiting**
  - Fast ingestion endpoint (`202 Accepted`).
  - IP/token rate-limiting.
  - Test-case bundle storage and caching strategies.
- [ ] **Phase 6: Kubernetes Manifests, Autoscaling & Load Testing**
  - Containerization of components.
  - Kubernetes manifests and security contexts.
  - Queue-depth autoscaling (KEDA/custom metrics).
  - Chaos testing (OOM evictions, node crashes, runaway submissions).
