package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sumith2425/sentinel-judge/internal/models"
)

type TestCase struct {
	Name            string
	ProblemID       string
	Language        models.Language
	Code            string
	TimeLimitMS     int
	MemoryLimitMB   int
	ExpectedVerdict models.SubmissionStatus
}

type submitResponse struct {
	SubmissionID string `json:"submission_id"`
	Status       string `json:"status"`
}

func main() {
	baseURL := "http://localhost:8080"
	if env := os.Getenv("API_URL"); env != "" {
		baseURL = env
	}

	tests := []TestCase{
		{
			Name:            "Language Parity: C++ Optimal Solution",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "#include <iostream>\nint main() { long long a, b; if (std::cin >> a >> b) std::cout << a + b << std::endl; return 0; }",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusAccepted,
		},
		{
			Name:            "Language Parity: Python 3 Optimal Solution",
			ProblemID:       "fibonacci",
			Language:        models.LangPython,
			Code:            "import sys\ndef main():\n    d = sys.stdin.read().split()\n    if not d: return\n    n = int(d[0])\n    a, b = 0, 1\n    for _ in range(n):\n        a, b = b, a + b\n    print(a)\nif __name__ == '__main__':\n    main()",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusAccepted,
		},
		{
			Name:            "Language Parity: Go Optimal Solution",
			ProblemID:       "prime_check",
			Language:        models.LangGo,
			Code:            "package main\nimport \"fmt\"\nfunc main() {\n    var n int64\n    if _, err := fmt.Scan(&n); err != nil { return }\n    if n < 2 { fmt.Println(\"COMPOSITE\"); return }\n    for d := int64(2); d*d <= n; d++ {\n        if n%d == 0 { fmt.Println(\"COMPOSITE\"); return }\n    }\n    fmt.Println(\"PRIME\")\n}",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusAccepted,
		},
		{
			Name:            "Algorithmic Failure: Logic Mismatch",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "#include <iostream>\nint main() { std::cout << -999999 << std::endl; return 0; }",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusWrongAnswer,
		},
		{
			Name:            "Compiler Diagnostics: C++ Syntax Error",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "#include <iostream>\nint main() { missing_semicolon_and_types return 0; }",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusCompilationError,
		},
		{
			Name:            "Sandbox Guardrail: CPU Timeout (Infinite Loop)",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "int main() { while (true) {} return 0; }",
			TimeLimitMS:     1500,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusTimeLimitExceeded,
		},
		{
			Name:            "Sandbox Guardrail: Memory Limit (OOM 137)",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "#include <vector>\n#include <iostream>\nint main() { std::vector<int> v(40 * 1024 * 1024, 1); std::cout << v[0] << std::endl; return 0; }",
			TimeLimitMS:     3000,
			MemoryLimitMB:   64,
			ExpectedVerdict: models.StatusMemoryLimitExceeded,
		},
		{
			Name:            "Security Defense: Network Exfiltration Probe",
			ProblemID:       "two_sum",
			Language:        models.LangPython,
			Code:            "import socket, sys\ns = socket.socket(socket.AF_INET, socket.SOCK_STREAM)\ns.settimeout(2.0)\ntry:\n    s.connect(('1.1.1.1', 80))\n    print('CONNECTED')\nexcept Exception as e:\n    print('BLOCKED')\n    sys.exit(1)",
			TimeLimitMS:     3000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusRuntimeError,
		},
		{
			Name:            "Security Defense: Filesystem Write Tampering",
			ProblemID:       "two_sum",
			Language:        models.LangPython,
			Code:            "import sys\ntry:\n    with open('/workspace/tamper.txt', 'w') as f:\n        f.write('tampered')\nexcept Exception:\n    sys.exit(1)",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusRuntimeError,
		},
		{
			Name:            "Security Defense: Fork Bomb (PID Quota)",
			ProblemID:       "two_sum",
			Language:        models.LangCPP,
			Code:            "#include <unistd.h>\nint main() { while (1) { fork(); } return 0; }",
			TimeLimitMS:     2000,
			MemoryLimitMB:   128,
			ExpectedVerdict: models.StatusTimeLimitExceeded,
		},
	}

	fmt.Println("========================================================================")
	fmt.Println("             SENTINELJUDGE INTENSIVE VERIFICATION SUITE                 ")
	fmt.Println("========================================================================")
	fmt.Printf("Target Endpoint: %s\n", baseURL)
	fmt.Printf("Total Scenarios: %d\n\n", len(tests))

	passed := 0
	startTotal := time.Now()

	for idx, tc := range tests {
		fmt.Printf("[%02d/%02d] %s\n", idx+1, len(tests), tc.Name)

		subID, err := submitCode(baseURL, tc)
		if err != nil {
			fmt.Printf("       FAILED: Submission rejected: %v\n\n", err)
			continue
		}

		finalStatus, details, execTime, err := pollStatus(baseURL, subID, 15*time.Second)
		if err != nil {
			fmt.Printf("       FAILED: Polling timeout/error: %v\n\n", err)
			continue
		}

		matched := finalStatus == tc.ExpectedVerdict
		if !matched && tc.Name == "Security Defense: Fork Bomb (PID Quota)" {
			if finalStatus == models.StatusRuntimeError {
				matched = true
			}
		}

		if matched {
			passed++
			fmt.Printf("       PASS -> Verdict: %s | Time: %dms\n", finalStatus, execTime)
			if details != "" {
				fmt.Printf("               Details: %s\n", truncate(details, 80))
			}
		} else {
			fmt.Printf("       FAIL -> Expected: %s | Received: %s\n", tc.ExpectedVerdict, finalStatus)
			if details != "" {
				fmt.Printf("               Details: %s\n", details)
			}
		}
		fmt.Println()
		time.Sleep(200 * time.Millisecond)
	}

	wallTime := time.Since(startTotal)
	fmt.Println("========================================================================")
	fmt.Printf("EXECUTION SUMMARY: %d/%d Passed (Wall Time: %v)\n", passed, len(tests), wallTime.Round(time.Millisecond))
	fmt.Println("========================================================================")

	if passed != len(tests) {
		os.Exit(1)
	}
}

func submitCode(baseURL string, tc TestCase) (string, error) {
	reqBody := models.SubmissionRequest{
		UserID:        "sec_auditor",
		QuestionID:    tc.ProblemID,
		Language:      tc.Language,
		Code:          tc.Code,
		TimeLimitMS:   tc.TimeLimitMS,
		MemoryLimitMB: tc.MemoryLimitMB,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(baseURL+"/api/v1/submit", "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	var res submitResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}
	return res.SubmissionID, nil
}

func pollStatus(baseURL, subID string, timeout time.Duration) (models.SubmissionStatus, string, int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/submissions/%s", baseURL, subID))
		if err == nil {
			var sub models.Submission
			if err := json.NewDecoder(resp.Body).Decode(&sub); err == nil {
				resp.Body.Close()
				if models.IsTerminalStatus(sub.Status) {
					details := ""
					if sub.VerdictDetails != nil {
						details = *sub.VerdictDetails
					}
					t := 0
					if sub.ExecutionTimeMS != nil {
						t = *sub.ExecutionTimeMS
					}
					return sub.Status, details, t, nil
				}
			} else {
				resp.Body.Close()
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", "", 0, fmt.Errorf("timeout awaiting terminal status")
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}