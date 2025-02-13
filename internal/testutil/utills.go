package testutil

import (
	"testing"
	"time"
)

func AssertExitsAfter(t *testing.T, desc string, fn func(), timeout time.Duration) bool {
	done := make(chan bool)
	go func() {
		fn()
		close(done)
	}()

	select {
	case <-done:
		break
	case <-time.After(timeout):
		t.Errorf("(%s) expected to terminate after %s", desc, timeout)
		return false
	}

	return true
}
