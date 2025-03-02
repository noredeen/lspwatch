package integration

import (
	"testing"
	"time"

	"github.com/noredeen/lspwatch/internal/testutil"
)

// TODO: Proper test cleanup.

func TestInvalidCommand(t *testing.T) {
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
}

func TestBadConfigFile(t *testing.T) {
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

func TestServerProcessDiesAbruptly(t *testing.T) {
	t.Parallel()
	logDir := testutil.GenerateRandomLogDirName()
	cmd := testutil.PrepareIntegrationTest(t, "--logdir", logDir, "--", "sleep", "5")

	err := cmd.Start()
	if err != nil {
		t.Fatalf("error starting lspwatch: %v", err)
	}

	testutil.AssertExitsBefore(t, "lspwatch", func() {
		cmd.Process.Wait()
	}, 15*time.Second)
}

func TestUnresponsiveServerProcess(t *testing.T) {
	t.Parallel()
	logDir := testutil.GenerateRandomLogDirName()
	cmd := testutil.PrepareIntegrationTest(t, "--logdir", logDir, "--", "./build/unresponsive_server")

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
		t.Fatalf("failed to write exit request: %v", err)
	}

	testutil.AssertExitsBefore(t, "lspwatch", func() {
		cmd.Process.Wait()
	}, 15*time.Second)

	// TODO: MUST CHECK THE LOGS AS PART OF THE TEST.
}
