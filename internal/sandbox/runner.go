package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sumith2425/sentinel-judge/internal/models"
)

type ProgressCallback func(status models.SubmissionStatus, currentTest, totalTests int)

type ExecutionResult struct {
	Verdict           models.SubmissionStatus
	VerdictDetails    *string
	ExecutionTimeMS   int
	ExecutionMemoryKB int
}

type Runner struct {
	ProblemsDir string
	WorkDirBase string
}

func NewRunner(problemsDir, workDirBase string) (*Runner, error) {
	absProb, err := filepath.Abs(problemsDir)
	if err != nil {
		return nil, err
	}
	absWork, err := filepath.Abs(workDirBase)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absWork, 0755); err != nil {
		return nil, err
	}
	return &Runner{
		ProblemsDir: absProb,
		WorkDirBase: absWork,
	}, nil
}

func (r *Runner) Evaluate(ctx context.Context, sub *models.Submission, onProgress ProgressCallback) *ExecutionResult {
	jobDir := filepath.Join(r.WorkDirBase, sub.SubmissionID.String())
	if err := os.MkdirAll(jobDir, 0777); err != nil {
		msg := fmt.Sprintf("failed to create workspace: %v", err)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}
	defer os.RemoveAll(jobDir)

	var sourceFile, image, compileCmd, runCmd string

	switch sub.Language {
	case models.LangCPP:
		sourceFile = "main.cpp"
		image = "frolvlad/alpine-gxx:latest"
		compileCmd = "g++ -O2 /workspace/main.cpp -o /workspace/solution"
		runCmd = "/workspace/solution"
	case models.LangPython:
		sourceFile = "main.py"
		image = "python:3.12-alpine"
		compileCmd = "python3 -m py_compile /workspace/main.py"
		runCmd = "python3 /workspace/main.py"
	case models.LangGo:
		sourceFile = "main.go"
		image = "golang:1.22-alpine"
		compileCmd = "GOCACHE=/tmp/gocache go build -o /workspace/solution /workspace/main.go"
		runCmd = "/workspace/solution"
	default:
		msg := fmt.Sprintf("unsupported language: %s", sub.Language)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}

	sourcePath := filepath.Join(jobDir, sourceFile)
	if err := os.WriteFile(sourcePath, []byte(sub.Code), 0644); err != nil {
		msg := fmt.Sprintf("failed to write source file: %v", err)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}

	testInputs, err := r.discoverTestCases(sub.QuestionID)
	if err != nil || len(testInputs) == 0 {
		msg := fmt.Sprintf("test cases not found for problem: %s", sub.QuestionID)
		return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
	}

	totalTests := len(testInputs)
	if onProgress != nil {
		onProgress(models.StatusCompiling, 0, totalTests)
	}

	compCtx, compCancel := context.WithTimeout(ctx, 10*time.Second)
	defer compCancel()

	compOut, compExitCode, compTimedOut := r.runDockerContainer(
		compCtx,
		image,
		jobDir,
		compileCmd,
		nil,
		512,
		false,
	)

	if compTimedOut {
		msg := "Compilation timed out (exceeded 10s)"
		return &ExecutionResult{Verdict: models.StatusCompilationError, VerdictDetails: &msg}
	}
	if compExitCode != 0 {
		msg := strings.TrimSpace(compOut)
		if msg == "" {
			msg = fmt.Sprintf("Compilation failed with exit code %d", compExitCode)
		}
		return &ExecutionResult{Verdict: models.StatusCompilationError, VerdictDetails: &msg}
	}

	maxTimeMS := 0
	for i, inPath := range testInputs {
		testNum := i + 1
		if onProgress != nil {
			onProgress(models.StatusRunning, testNum, totalTests)
		}

		outPath := strings.TrimSuffix(inPath, ".in") + ".out"
		inputBytes, err := os.ReadFile(inPath)
		if err != nil {
			msg := fmt.Sprintf("failed to read input for test %d", testNum)
			return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
		}
		expectedBytes, err := os.ReadFile(outPath)
		if err != nil {
			msg := fmt.Sprintf("failed to read expected output for test %d", testNum)
			return &ExecutionResult{Verdict: models.StatusInternalError, VerdictDetails: &msg}
		}

		testTimeout := time.Duration(sub.TimeLimitMS) * time.Millisecond
		runCtx, runCancel := context.WithTimeout(ctx, testTimeout)

		start := time.Now()
		actualOut, exitCode, timedOut := r.runDockerContainer(
			runCtx,
			image,
			jobDir,
			runCmd,
			inputBytes,
			sub.MemoryLimitMB,
			true,
		)
		elapsedMS := int(time.Since(start).Milliseconds())
		runCancel()

		if elapsedMS > maxTimeMS {
			maxTimeMS = elapsedMS
		}

		if timedOut {
			msg := fmt.Sprintf("Time Limit Exceeded on test case %d/%d", testNum, totalTests)
			return &ExecutionResult{
				Verdict:         models.StatusTimeLimitExceeded,
				VerdictDetails:  &msg,
				ExecutionTimeMS: sub.TimeLimitMS,
			}
		}

		if exitCode == 137 {
			msg := fmt.Sprintf("Memory Limit Exceeded on test case %d/%d", testNum, totalTests)
			return &ExecutionResult{
				Verdict:           models.StatusMemoryLimitExceeded,
				VerdictDetails:    &msg,
				ExecutionTimeMS:   maxTimeMS,
				ExecutionMemoryKB: sub.MemoryLimitMB * 1024,
			}
		}

		if exitCode != 0 {
			msg := fmt.Sprintf("Runtime Error on test case %d/%d (exit code %d): %s", testNum, totalTests, exitCode, strings.TrimSpace(actualOut))
			return &ExecutionResult{
				Verdict:         models.StatusRuntimeError,
				VerdictDetails:  &msg,
				ExecutionTimeMS: maxTimeMS,
			}
		}

		if strings.TrimSpace(actualOut) != strings.TrimSpace(string(expectedBytes)) {
			msg := fmt.Sprintf("Wrong Answer on test case %d/%d: expected %q, got %q", testNum, totalTests, strings.TrimSpace(string(expectedBytes)), strings.TrimSpace(actualOut))
			return &ExecutionResult{
				Verdict:         models.StatusRuntimeError,
				VerdictDetails:  &msg,
				ExecutionTimeMS: maxTimeMS,
			}
		}
	}

	msg := fmt.Sprintf("Passed all %d test cases", totalTests)
	return &ExecutionResult{
		Verdict:         models.StatusAccepted,
		VerdictDetails:  &msg,
		ExecutionTimeMS: maxTimeMS,
	}
}

func (r *Runner) discoverTestCases(questionID string) ([]string, error) {
	dir := filepath.Join(r.ProblemsDir, questionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var inputs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".in") {
			inputs = append(inputs, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(inputs)
	return inputs, nil
}

func (r *Runner) runDockerContainer(
	ctx context.Context,
	image string,
	jobDir string,
	command string,
	stdinData []byte,
	memoryMB int,
	readOnlyWorkspace bool,
) (string, int, bool) {
	containerName := fmt.Sprintf("sbx-%d", time.Now().UnixNano())
	mountMode := "rw"
	if readOnlyWorkspace {
		mountMode = "ro"
	}

	memFlag := fmt.Sprintf("%dm", memoryMB)
	args := []string{
		"run",
		"--rm",
		"-i",
		"--name", containerName,
		"--network", "none",
		"--memory", memFlag,
		"--memory-swap", memFlag,
		"--cpus", "1.0",
		"--pids-limit", "64",
		"-v", fmt.Sprintf("%s:/workspace:%s", jobDir, mountMode),
		image,
		"sh", "-c", command,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	if stdinData != nil {
		cmd.Stdin = bytes.NewReader(stdinData)
	}

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	err := cmd.Run()

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		return outBuf.String(), -1, true
	}

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return outBuf.String(), exitErr.ExitCode(), false
		}
		return err.Error(), -1, false
	}

	return outBuf.String(), 0, false
}