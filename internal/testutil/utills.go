package testutil

import (
	"testing"
	"time"
)

// TODO: Tests.

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
