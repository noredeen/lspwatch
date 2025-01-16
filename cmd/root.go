package cmd

import (
	"fmt"
	"lspwatch/internal"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
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
}

func createLogger() (*logrus.Logger, *os.File) {
	logger := logrus.New()
	file, err := os.OpenFile("lspwatch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// TODO: Eventually logging should be optional
		logger.Fatalf("Error creating log file: %v", err)
	}

	logger.Out = file

	return logger, file
}

func launchInterruptListener(serverCmd *exec.Cmd, logger *logrus.Logger) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		for sig := range signalChan {
			logger.Infof("lspwatch process interrupted. forwarding signal to language server...")
			err := serverCmd.Process.Signal(sig)
			if err != nil {
				logger.Fatalf(
					"error forwarding signal to language server process (PID=%v): %v",
					serverCmd.Process.Pid,
					err,
				)
			}
		}
	}()
}

func launchProcessExitListener(serverCmd *exec.Cmd, outgoingStopSignal chan error, logger *logrus.Logger) {
	go func() {
		err := serverCmd.Wait()
		outgoingStopSignal <- err
	}()
}

func RunProxy(serverShellCommand string, args []string) {
	logger, logFile := createLogger()

	logger.Info("starting lspwatch...")

	requestsHandler, err := internal.NewRequestsHandler(logger)
	if err != nil {
		logger.Fatalf("error initializing LSP request handler: %v", err)
	}

	serverCmd := exec.Command(serverShellCommand, args...)

	stdoutPipe, err := serverCmd.StdoutPipe()
	if err != nil {
		logger.Fatalf("error creating pipe to server's stdout: %v", err)
	}

	stdinPipe, err := serverCmd.StdinPipe()
	if err != nil {
		logger.Fatalf("error creating pipe to server's stdin: %v", err)
	}

	logger.Infof("starting language server using command '%v' and args '%v'", serverShellCommand, args)
	err = serverCmd.Start()
	if err != nil {
		logger.Fatalf("error starting language server process: %v", err)
	}

	logger.Infof("launched language server process (PID=%v)", serverCmd.Process.Pid)

	launchInterruptListener(serverCmd, logger)

	processExitChan := make(chan error)
	launchProcessExitListener(serverCmd, processExitChan, logger)

	var listenersWaitGroup sync.WaitGroup
	listenersWaitGroup.Add(2)
	listenersStopChan := make(chan struct{})
	go requestsHandler.ListenServer(stdoutPipe, logger, listenersStopChan, &listenersWaitGroup)
	go requestsHandler.ListenClient(stdinPipe, logger, listenersStopChan, &listenersWaitGroup)

	exitCode := 0

	defer func() {
		// Wait for all server and client listeners to exit
		listenersWaitGroup.Wait()

		stdoutPipe.Close()
		stdinPipe.Close()

		logger.Info("lspwatch shutdown cleanup complete. goodbye!")

		logFile.Close()
		os.Exit(exitCode)
	}()

	for {
		select {
		case err = <-processExitChan:
			{
				logger.Info("language server process exited")

				if err != nil {
					exitCode = serverCmd.ProcessState.ExitCode()
				}

				if listenersStopChan != nil {
					// Ask the listeners to terminate
					close(listenersStopChan)
					listenersStopChan = nil
				}

				return
			}
		case <-listenersStopChan:
			// Either client or server listener requested shutdown
			{
				if listenersStopChan != nil {
					err = serverCmd.Process.Signal(syscall.SIGINT)
					if err != nil {
						logger.Fatalf("error signaling language server to shut down: %v", err)
					}

					// Ask the other listener(s) to terminate
					close(listenersStopChan)
					listenersStopChan = nil
				}
			}
		}
	}
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
