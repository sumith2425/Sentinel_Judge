package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sumith2425/sentinel-judge/internal/models"
)

type submitResponse struct {
	SubmissionID string `json:"submission_id"`
	Status       string `json:"status"`
}

type statusResponse struct {
	SubmissionID string                  `json:"submission_id"`
	Status       models.SubmissionStatus `json:"status"`
}

func main() {
	apiURL := "http://localhost:8080"
	totalRequests := 20

	fmt.Printf("Starting SentinelJudge Load Test (%d concurrent submissions)...\n", totalRequests)

	var wg sync.WaitGroup
	ingestLatencies := make([]time.Duration, totalRequests)
	submissionIDs := make([]string, totalRequests)
	var successIngest int32

	startAll := time.Now()

	for i := 0; i < totalRequests; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			reqBody := models.SubmissionRequest{
				UserID:        fmt.Sprintf("load_user_%d", idx),
				QuestionID:    "two_sum",
				Language:      models.LangCPP,
				Code:          "#include <iostream>\nint main() { int a, b; std::cin >> a >> b; std::cout << a + b << std::endl; return 0; }",
				TimeLimitMS:   2000,
				MemoryLimitMB: 128,
			}

			payload, _ := json.Marshal(reqBody)
			reqStart := time.Now()

			resp, err := http.Post(apiURL+"/api/v1/submit", "application/json", bytes.NewReader(payload))
			ingestLatencies[idx] = time.Since(reqStart)

			if err != nil {
				fmt.Printf("[Req %d] HTTP error: %v\n", idx, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusAccepted {
				fmt.Printf("[Req %d] Unexpected status code: %d\n", idx, resp.StatusCode)
				return
			}

			var subResp submitResponse
			if err := json.NewDecoder(resp.Body).Decode(&subResp); err == nil {
				submissionIDs[idx] = subResp.SubmissionID
				atomic.AddInt32(&successIngest, 1)
			}
		}(i)
	}

	wg.Wait()
	ingestTotalDuration := time.Since(startAll)

	sort.Slice(ingestLatencies, func(i, j int) bool {
		return ingestLatencies[i] < ingestLatencies[j]
	})

	p50 := ingestLatencies[totalRequests*50/100]
	p95 := ingestLatencies[totalRequests*95/100]
	p99 := ingestLatencies[totalRequests*99/100]

	fmt.Println("\n--- INGESTION REPORT ---")
	fmt.Printf("Accepted Submissions : %d / %d\n", successIngest, totalRequests)
	fmt.Printf("Total Ingest Window  : %v\n", ingestTotalDuration.Round(time.Millisecond))
	fmt.Printf("p50 Latency          : %v\n", p50.Round(time.Millisecond))
	fmt.Printf("p95 Latency          : %v\n", p95.Round(time.Millisecond))
	fmt.Printf("p99 Latency          : %v\n", p99.Round(time.Millisecond))

	fmt.Println("\nWaiting for Worker Pool to finish grading all queued submissions...")

	completed := make(map[string]models.SubmissionStatus)
	for len(completed) < int(successIngest) {
		for _, id := range submissionIDs {
			if id == "" {
				continue
			}
			if _, done := completed[id]; done {
				continue
			}

			resp, err := http.Get(fmt.Sprintf("%s/api/v1/submissions/%s", apiURL, id))
			if err != nil {
				continue
			}
			var st statusResponse
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()

			if st.Status == models.StatusAccepted ||
				st.Status == models.StatusTimeLimitExceeded ||
				st.Status == models.StatusMemoryLimitExceeded ||
				st.Status == models.StatusRuntimeError ||
				st.Status == models.StatusCompilationError ||
				st.Status == models.StatusInternalError {
				completed[id] = st.Status
				fmt.Printf("   Graded [%d/%d]: %s -> %s\n", len(completed), successIngest, id, st.Status)
			}
		}
		if len(completed) < int(successIngest) {
			time.Sleep(500 * time.Millisecond)
		}
	}

	totalE2E := time.Since(startAll)
	fmt.Println("\n--- END-TO-END GRADING REPORT ---")
	fmt.Printf("Total Graded         : %d / %d\n", len(completed), successIngest)
	fmt.Printf("Total E2E Wall Time  : %v\n", totalE2E.Round(time.Millisecond))
	fmt.Printf("Average Throughput   : %.2f submissions/sec\n", float64(len(completed))/totalE2E.Seconds())
}