package cmd

import (
	"fmt"
	"lspwatch/internal"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// NOTE: This is probably a little broken on Windows
// TODO: Make request buffer thread-safe?

var rootCmd = &cobra.Command{
	Use:   "lspwatch",
	Short: "lspwatch provides observability for LSP-compliant language servers over stdin/stdout",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) > 1 {
			RunProxy(args[0], args[1:])
		} else {
			RunProxy(args[0], []string{})
		}
	},
	DisableFlagParsing: true,
}

func createLogger() (*log.Logger, *os.File) {
	logger := log.New()
	file, err := os.OpenFile("lspwatch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// TODO: Eventually logging should be optional
		logger.Fatal("Failed to create log file")
	}

	logger.Out = file

	return logger, file
}

func launchInterruptHandler(serverCmd *exec.Cmd, logger *log.Logger) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		sig := <-signalChan
		logger.Infof("lspwatch process interrupted, forwarding signal to language server...")
		err := serverCmd.Process.Signal(sig)
		if err != nil {
			logger.Fatalf(
				"Failed to forward signal to language server process (PID=%v): %v",
				serverCmd.Process.Pid,
				err,
			)
		}
	}()
}

func RunProxy(serverShellCommand string, args []string) {
	logger, logFile := createLogger()

	logger.Info("Starting lspwatch...")

	requestsHandler := internal.NewRequestsHandler()

	serverCmd := exec.Command(serverShellCommand, args...)

	stdoutPipe, err := serverCmd.StdoutPipe()
	if err != nil {
		logger.Fatalf("Failed to create pipe to server's stdout: %v", err)
	}

	stdinPipe, err := serverCmd.StdinPipe()
	if err != nil {
		logger.Fatalf("Failed to create pipe to server's stdin: %v", err)
	}

	logger.Infof("Starting language server using command '%v' and args '%v'", serverShellCommand, args)
	err = serverCmd.Start()
	if err != nil {
		logger.Fatalf("Failed to start language server process: %v", err)
	}

	logger.Infof("Launched language server process (PID=%v)", serverCmd.Process.Pid)

	launchInterruptHandler(serverCmd, logger)

	go requestsHandler.ListenServer(stdoutPipe, logger)
	go requestsHandler.ListenClient(stdinPipe, logger)

	// TODO: I really don't like this at all
	// https://github.com/golang/go/issues/26539
	exitCode := 0
	err = serverCmd.Wait()
	if err != nil {
		logger.Error("Language server has terminated with non-zero exit code")
		exitCode = serverCmd.ProcessState.ExitCode()
	} else {
		logger.Info("Langauge server exited successfully")
	}

	logFile.Close()
	stdoutPipe.Close()
	stdinPipe.Close()

	os.Exit(exitCode)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
