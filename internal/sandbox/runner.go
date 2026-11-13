package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sumith2425/sentinel-judge/internal/models"
)

type ProgressCallback func(status models.SubmissionStatus, currentTest int, totalTests int)

type ExecutionResult struct {
	Verdict           models.SubmissionStatus
	VerdictDetails    *string
	ExecutionTimeMS   int
	ExecutionMemoryKB int
}

type Runner struct {
	ProblemsDir string
	WorkDir     string
}

func NewRunner(problemsDir, workDir string) *Runner {
	return &Runner{
		ProblemsDir: problemsDir,
		WorkDir:     workDir,
	}
}

func (r *Runner) Evaluate(ctx context.Context, sub *models.Submission, onProgress ProgressCallback) *ExecutionResult {
	jobDir := filepath.Join(r.WorkDir, sub.SubmissionID.String())
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		msg := fmt.Sprintf("Failed to create sandbox directory: %v", err)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}
	defer os.RemoveAll(jobDir)

	var sourceFile, compileCmd, runCmd, image string

	switch sub.Language {
	case models.LangCPP:
		image = "frolvlad/alpine-gxx:latest"
		sourceFile = "main.cpp"
		compileCmd = "g++ -O2 -std=c++20 /workspace/main.cpp -o /workspace/solution"
		runCmd = "/workspace/solution"
	case models.LangPython:
		image = "python:3.12-alpine"
		sourceFile = "main.py"
		compileCmd = "python3 -m py_compile /workspace/main.py"
		runCmd = "python3 /workspace/main.py"
	case models.LangGo:
		image = "golang:1.22-alpine"
		sourceFile = "main.go"
		compileCmd = "GOCACHE=/tmp/gocache go build -o /workspace/solution /workspace/main.go"
		runCmd = "/workspace/solution"
	default:
		msg := fmt.Sprintf("Unsupported language: %s", sub.Language)
		return &ExecutionResult{Verdict: models.StatusCompilationError, VerdictDetails: &msg}
	}

	sourcePath := filepath.Join(jobDir, sourceFile)
	if err := os.WriteFile(sourcePath, []byte(sub.Code), 0644); err != nil {
		msg := fmt.Sprintf("Failed to write source code: %v", err)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}

	if onProgress != nil {
		onProgress(models.StatusCompiling, 0, 0)
	}

	compCtx, compCancel := context.WithTimeout(ctx, 30*time.Second)
	defer compCancel()

	compOut, compExitCode, compTimedOut := r.runOneShotContainer(
		compCtx,
		image,
		jobDir,
		compileCmd,
		512,
		512,
	)

	if compTimedOut {
		msg := "Compilation timed out (exceeded 30s)"
		return &ExecutionResult{Verdict: models.StatusCompilationError, VerdictDetails: &msg}
	}
	if compExitCode != 0 {
		msg := truncateOutput(compOut, 2048)
		return &ExecutionResult{Verdict: models.StatusCompilationError, VerdictDetails: &msg}
	}

	problemPath := filepath.Join(r.ProblemsDir, sub.QuestionID)
	inputFiles, err := filepath.Glob(filepath.Join(problemPath, "*.in"))
	if err != nil || len(inputFiles) == 0 {
		msg := fmt.Sprintf("No test cases found for problem %s", sub.QuestionID)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}
	sortTestFiles(inputFiles)

	totalTests := len(inputFiles)
	maxTimeMS := 0

	execContainerName := fmt.Sprintf("sbx-exec-%s", uuid.New().String()[:12])
	if err := r.startPersistentSandbox(ctx, execContainerName, image, jobDir, sub.MemoryLimitMB, 64); err != nil {
		msg := fmt.Sprintf("Failed to initialize execution sandbox: %v", err)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}
	defer func() {
		_ = exec.Command("docker", "rm", "-f", execContainerName).Run()
	}()

	timeoutSec := float64(sub.TimeLimitMS) / 1000.0
	if timeoutSec < 0.1 {
		timeoutSec = 1.0
	}

	for i, inFile := range inputFiles {
		testNum := i + 1
		if onProgress != nil {
			onProgress(models.StatusRunning, testNum, totalTests)
		}

		expectedFile := strings.TrimSuffix(inFile, ".in") + ".out"
		expectedBytes, err := os.ReadFile(expectedFile)
		if err != nil {
			msg := fmt.Sprintf("Missing expected output file for test case %d", testNum)
			return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
		}

		inputBytes, err := os.ReadFile(inFile)
		if err != nil {
			msg := fmt.Sprintf("Failed to read input file for test case %d", testNum)
			return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
		}

		execCmd := fmt.Sprintf("timeout -s KILL %.3fs %s", timeoutSec, runCmd)
		start := time.Now()
		actualOut, exitCode := r.execInSandbox(ctx, execContainerName, execCmd, inputBytes)
		elapsedMS := int(time.Since(start).Milliseconds())
		if elapsedMS > maxTimeMS {
			maxTimeMS = elapsedMS
		}

		if exitCode == 137 {
			if r.isContainerOOMKilled(execContainerName) || elapsedMS < sub.TimeLimitMS {
				msg := fmt.Sprintf("Memory Limit Exceeded on test case %d/%d", testNum, totalTests)
				return &ExecutionResult{
					Verdict:           models.StatusMemoryLimitExceeded,
					VerdictDetails:    &msg,
					ExecutionTimeMS:   elapsedMS,
					ExecutionMemoryKB: sub.MemoryLimitMB * 1024,
				}
			}
			msg := fmt.Sprintf("Time Limit Exceeded on test case %d/%d", testNum, totalTests)
			return &ExecutionResult{
				Verdict:         models.StatusTimeLimitExceeded,
				VerdictDetails:  &msg,
				ExecutionTimeMS: sub.TimeLimitMS,
			}
		}

		if exitCode == 124 || elapsedMS >= sub.TimeLimitMS {
			msg := fmt.Sprintf("Time Limit Exceeded on test case %d/%d", testNum, totalTests)
			return &ExecutionResult{
				Verdict:         models.StatusTimeLimitExceeded,
				VerdictDetails:  &msg,
				ExecutionTimeMS: sub.TimeLimitMS,
			}
		}

		if exitCode != 0 {
			msg := fmt.Sprintf("Runtime Error on test case %d/%d (exit code %d): %s", testNum, totalTests, exitCode, truncateOutput(actualOut, 1024))
			return &ExecutionResult{
				Verdict:         models.StatusRuntimeError,
				VerdictDetails:  &msg,
				ExecutionTimeMS: elapsedMS,
			}
		}

		if normalizeOutput(actualOut) != normalizeOutput(string(expectedBytes)) {
			msg := fmt.Sprintf("Wrong Answer on test case %d/%d: expected %q, got %q", testNum, totalTests, truncateOutput(normalizeOutput(string(expectedBytes)), 200), truncateOutput(normalizeOutput(actualOut), 200))
			return &ExecutionResult{
				Verdict:         models.StatusWrongAnswer,
				VerdictDetails:  &msg,
				ExecutionTimeMS: maxTimeMS,
			}
		}
	}

	successMsg := fmt.Sprintf("Passed all %d test cases", totalTests)
	return &ExecutionResult{
		Verdict:         models.StatusAccepted,
		VerdictDetails:  &successMsg,
		ExecutionTimeMS: maxTimeMS,
	}
}

func (r *Runner) runOneShotContainer(
	ctx context.Context,
	image string,
	jobDir string,
	command string,
	memoryMB int,
	pidsLimit int,
) (string, int, bool) {
	containerName := fmt.Sprintf("sbx-comp-%s", uuid.New().String()[:12])
	memFlag := fmt.Sprintf("%dm", memoryMB)

	args := []string{
		"run",
		"--rm",
		"--name", containerName,
		"--network", "none",
		"--memory", memFlag,
		"--memory-swap", memFlag,
		"--cpus", "1.0",
		"--pids-limit", fmt.Sprintf("%d", pidsLimit),
		"-v", fmt.Sprintf("%s:/workspace:rw", jobDir),
		"-v", "sentinel-gocache:/tmp/gocache",
		image,
		"sh", "-c", command,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		return outBuf.String(), -1, true
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return outBuf.String(), exitCode, false
}

func (r *Runner) startPersistentSandbox(
	ctx context.Context,
	containerName string,
	image string,
	jobDir string,
	memoryMB int,
	pidsLimit int,
) error {
	memFlag := fmt.Sprintf("%dm", memoryMB)
	args := []string{
		"run",
		"-d",
		"--rm",
		"--name", containerName,
		"--network", "none",
		"--memory", memFlag,
		"--memory-swap", memFlag,
		"--cpus", "1.0",
		"--pids-limit", fmt.Sprintf("%d", pidsLimit),
		"-v", fmt.Sprintf("%s:/workspace:ro", jobDir),
		image,
		"sleep", "300",
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, errBuf.String())
	}
	return nil
}

func (r *Runner) execInSandbox(
	ctx context.Context,
	containerName string,
	command string,
	stdinData []byte,
) (string, int) {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", containerName, "sh", "-c", command)
	cmd.Stdin = bytes.NewReader(stdinData)

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return outBuf.String(), exitCode
}

func (r *Runner) isContainerOOMKilled(containerName string) bool {
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.OOMKilled}}", containerName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func sortTestFiles(files []string) {
	sort.Slice(files, func(i, j int) bool {
		baseI := strings.TrimSuffix(filepath.Base(files[i]), ".in")
		baseJ := strings.TrimSuffix(filepath.Base(files[j]), ".in")
		numI, errI := strconv.Atoi(baseI)
		numJ, errJ := strconv.Atoi(baseJ)
		if errI == nil && errJ == nil {
			return numI < numJ
		}
		return files[i] < files[j]
	})
}

func normalizeOutput(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func truncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "\n... [output truncated]"
}