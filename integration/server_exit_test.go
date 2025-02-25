package integration

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TODO: scenario: forwarding the interrupt signal fails

func TestServerProcessDiesAbruptly(t *testing.T) {
	t.Parallel()
	lspwatchBinary := os.Getenv("LSPWATCH_BIN")
	if lspwatchBinary == "" {
		t.Fatalf("LSPWATCH_BIN is not set")
	}

	coverageDir := os.Getenv("COVERAGE_DIR")
	if coverageDir == "" {
		t.Fatalf("COVERAGE_DIR is not set")
	}

	t.Logf("Starting lspwatch binary '%s'", lspwatchBinary)

	// Launch lspwatch process.
	cmd := exec.Command(
		lspwatchBinary,
		"--",
		"sleep",
		"5",
	)

	coverPath := fmt.Sprintf("GOCOVERDIR=%s", coverageDir)
	t.Logf("Sending coverage data to: %s", coverageDir)
	cmd.Env = append(os.Environ(), coverPath)

	err := cmd.Start()
	if err != nil {
		t.Fatalf("error starting lspwatch: %v", err)
	}

	done := make(chan struct{})
	go func() {
		cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		break
	case <-time.After(15 * time.Second):
		t.Errorf("lspwatch did not exit after 15 seconds")
	}
}

func TestUnresponsiveServerProcess(t *testing.T) {
	t.Parallel()
	lspwatchBinary := os.Getenv("LSPWATCH_BIN")
	if lspwatchBinary == "" {
		t.Fatalf("LSPWATCH_BIN is not set")
	}

	coverageDir := os.Getenv("COVERAGE_DIR")
	if coverageDir == "" {
		t.Fatalf("COVERAGE_DIR is not set")
	}

	t.Logf("Starting lspwatch binary '%s'", lspwatchBinary)

	// Launch lspwatch process.
	cmd := exec.Command(
		lspwatchBinary,
		"--",
		"./build/unresponsive_server",
	)

	serverStdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("failed to create stdin pipe: %v", err)
	}
	serverStdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}

	coverPath := fmt.Sprintf("GOCOVERDIR=%s", coverageDir)
	t.Logf("Sending coverage data to: %s", coverageDir)
	cmd.Env = append(os.Environ(), coverPath)

	err = cmd.Start()
	if err != nil {
		t.Fatalf("error starting lspwatch: %v", err)
	}

	go func() {
		for {
			buf := make([]byte, 1024)
			serverStdout.Read(buf)
		}
	}()

	time.Sleep(2 * time.Second)

	exitRequest := []byte("Content-Length: 33\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"exit\"}")
	_, err = serverStdin.Write(exitRequest)
	if err != nil {
		t.Fatalf("failed to write exit request: %v", err)
	}

	done := make(chan struct{})
	go func() {
		cmd.Process.Wait()
		close(done)
	}()
	select {
	case <-done:
		break
	case <-time.After(15 * time.Second):
		t.Errorf("lspwatch did not exit after 15 seconds")
	}
}
