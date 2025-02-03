package testutil

import (
	"testing"
	"time"
)

func AssertExitsAfter(t *testing.T, fn func(), timeout time.Duration) {
	done := make(chan bool)
	go func() {
		fn()
		close(done)
	}()

	select {
	case <-done:
		break
	case <-time.After(timeout):
		t.Errorf("expected exit after %s", timeout)
	}
}
