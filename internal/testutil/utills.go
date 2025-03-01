package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// AssertExitsAfter returns true if the function terminates after the timeout.
// WARNING: The goroutine running fn will not be terminated when this function returns.
// Code in fn may run beyond the timeout. Caller is responsible for stopping work in fn.
func AssertExitsBefore(t *testing.T, desc string, fn func(), timeout time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()

	deadline := time.After(timeout)
	select {
	case <-done:
		return true
	case <-deadline:
		t.Errorf("(%s) expected to terminate before %s", desc, timeout)
		return false
	}
}

// AssertDoesNotExitBefore returns true if the function does not terminate before the timeout.
// WARNING: The goroutine running fn will not be terminated when this function returns.
// Code in fn may run beyond the timeout. Caller is responsible for stopping work in fn.
func AssertDoesNotExitBefore(t *testing.T, desc string, fn func(), timeout time.Duration) bool {
	t.Helper()
	done := make(chan struct{})

	go func() {
		fn()
		close(done)
	}()

	deadline := time.After(timeout)
	select {
	case <-done:
		t.Errorf("(%s) expected NOT to terminate before %s", desc, timeout)
		return false
	case <-deadline:
		return true
	}
}

func RunIntegrationTest(t *testing.T, args []string, fn func(cmd *exec.Cmd)) {
	lspwatchBinary := os.Getenv("LSPWATCH_BIN")
	if lspwatchBinary == "" {
		t.Fatalf("LSPWATCH_BIN is not set")
	}

	coverageDir := os.Getenv("COVERAGE_DIR")
	if coverageDir == "" {
		t.Fatalf("COVERAGE_DIR is not set")
	}

	cmd := exec.Command(
		lspwatchBinary,
		args...,
	)

	coverPath := fmt.Sprintf("GOCOVERDIR=%s", coverageDir)
	t.Logf("Sending coverage data to: %s", coverageDir)
	cmd.Env = append(os.Environ(), coverPath)

	fn(cmd)
}
