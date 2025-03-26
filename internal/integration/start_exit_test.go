package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noredeen/lspwatch/internal/testutil"
)

// TODO: Proper test cleanup (for when tests fail and lspwatch doesn't shut down cleanly).

func TestInvalidCommand(t *testing.T) {
	t.Run("no command provided", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		// No language server command provided.
		cmd := testutil.PrepareIntegrationTest(t, "--logdir", logDir)

		err := cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 3*time.Second)

		if cmd.ProcessState.ExitCode() == 0 {
			t.Error("expected non-zero exit code")
		}
	})

	t.Run("nonexistent command", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		// No language server command provided.
		cmd := testutil.PrepareIntegrationTest(t, "--logdir", logDir, "--", "nonexistent_command")

		err := cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 3*time.Second)

		if cmd.ProcessState.ExitCode() == 0 {
			t.Error("expected non-zero exit code")
		}
	})
}

func TestBadConfigFile(t *testing.T) {
	// TODO: Check logs.
	t.Run("unreadable config file", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		cmd := testutil.PrepareIntegrationTest(
			t,
			"--logdir",
			logDir,
			"--config",
			"/dev/null",
			"--",
			"some_command",
		)

		err := cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 3*time.Second)

		if cmd.ProcessState.ExitCode() == 0 {
			t.Error("expected non-zero exit code")
		}
	})

	// TODO: Check logs.
	t.Run("nonexistent env file", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		cmd := testutil.PrepareIntegrationTest(
			t,
			"--logdir",
			logDir,
			"--config",
			"./config/bad_env_lspwatch.yaml",
			"--",
			"some_command",
		)

		err := cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 3*time.Second)

		if cmd.ProcessState.ExitCode() == 0 {
			t.Error("expected non-zero exit code")
		}
	})
}

func TestInvalidMode(t *testing.T) {
	t.Parallel()
	logDir := testutil.GenerateRandomLogDirName()
	// No language server command provided.
	cmd := testutil.PrepareIntegrationTest(
		t,
		"--logdir",
		logDir,
		"--mode",
		"invalid_mode",
		"--",
		"sleep",
		"5",
	)
	err := cmd.Start()
	if err != nil {
		t.Fatalf("error starting lspwatch: %v", err)
	}

	testutil.AssertExitsBefore(t, "lspwatch", func() {
		cmd.Process.Wait()
	}, 3*time.Second)

	if cmd.ProcessState.ExitCode() == 0 {
		t.Error("expected non-zero exit code")
	}

	time.Sleep(2 * time.Second)

	fileBytes, err := os.ReadFile(filepath.Join(logDir, "lspwatch.log"))
	if err != nil {
		t.Fatalf("error reading lspwatch.log: %v", err)
	}

	ok := bytes.Contains(fileBytes, []byte("invalid mode: 'invalid_mode'"))
	if !ok {
		t.Log(string(fileBytes))
		t.Fatalf("expected log message \"invalid mode: 'invalid_mode'\" not found in lspwatch.log")
	}
}

func TestServerProcessDiesAbruptly(t *testing.T) {
	t.Parallel()
	logDir := testutil.GenerateRandomLogDirName()
	cmd := testutil.PrepareIntegrationTest(
		t,
		"--logdir",
		logDir,
		"--mode",
		"proxy",
		"--",
		"sleep",
		"5",
	)

	err := cmd.Start()
	if err != nil {
		t.Fatalf("error starting lspwatch: %v", err)
	}

	testutil.AssertExitsBefore(t, "lspwatch", func() {
		cmd.Process.Wait()
	}, 15*time.Second)

	time.Sleep(2 * time.Second)

	fileBytes, err := os.ReadFile(filepath.Join(logDir, "lspwatch.log"))
	if err != nil {
		t.Fatalf("error reading lspwatch.log: %v", err)
	}

	ok := bytes.Contains(fileBytes, []byte("language server process exited"))
	if !ok {
		t.Fatalf("expected log message 'language server process exited' not found in lspwatch.log")
	}
}

func TestUnresponsiveServerProcess(t *testing.T) {
	t.Parallel()
	logDir := testutil.GenerateRandomLogDirName()
	cmd := testutil.PrepareIntegrationTest(t,
		"--logdir",
		logDir,
		"--mode",
		"proxy",
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
		t.Fatalf("error writing exit request: %v", err)
	}

	testutil.AssertExitsBefore(t, "lspwatch", func() {
		cmd.Process.Wait()
	}, 15*time.Second)

	// Wait for logs to flush.
	time.Sleep(1 * time.Second)

	fileBytes, err := os.ReadFile(filepath.Join(logDir, "lspwatch.log"))
	if err != nil {
		t.Fatalf("error reading lspwatch.log: %v", err)
	}

	ok := bytes.Contains(fileBytes, []byte("received exit request from client"))
	if !ok {
		t.Fatalf("expected log message 'received exit request from client' not found in lspwatch.log")
	}

	ok = bytes.Contains(fileBytes, []byte("organic language server shutdown failed. forcing with SIGKILL..."))
	if !ok {
		t.Fatalf("expected log message 'organic language server shutdown failed. forcing with SIGKILL...' not found in lspwatch.log")
	}
}

func TestCommandMode(t *testing.T) {
	t.Run("explicit mode", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		cmd := testutil.PrepareIntegrationTest(
			t,
			"--logdir",
			logDir,
			"--mode",
			"command",
			"--",
			"echo",
			"-n", // Suppress newline in output.
			"integration",
		)

		serverStdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("failed to create stdout pipe: %v", err)
		}

		err = cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		buf := make([]byte, 100)
		n, err := serverStdout.Read(buf)
		if err != nil {
			t.Fatalf("error reading stdout: %v", err)
		}

		if string(buf[:n]) != "integration" {
			t.Errorf("expected 'integration' in command stdout but found '%s'", string(buf[:n]))
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 2*time.Second)
	})

	t.Run("automatic commmand mode detection", func(t *testing.T) {
		t.Parallel()
		logDir := testutil.GenerateRandomLogDirName()
		cmd := testutil.PrepareIntegrationTest(
			t,
			"--logdir",
			logDir,
			"--",
			"echo",
			"-n", // Suppress newline in output.
			"integration",
		)

		serverStdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("failed to create stdout pipe: %v", err)
		}

		err = cmd.Start()
		if err != nil {
			t.Fatalf("error starting lspwatch: %v", err)
		}

		buf := make([]byte, 100)
		n, err := serverStdout.Read(buf)
		if err != nil {
			t.Fatalf("error reading stdout: %v", err)
		}

		if string(buf[:n]) != "integration" {
			t.Fatalf("expected 'integration' not found in command stdout but found '%s'", string(buf[:n]))
		}

		testutil.AssertExitsBefore(t, "lspwatch", func() {
			cmd.Process.Wait()
		}, 2*time.Second)
	})
}
