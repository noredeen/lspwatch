package cmd

import (
	"fmt"
	"io"
	"lspwatch/internal"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// TODO:
// - [x] support -c or --config-file
// - [x] finish datadog exporter
// - [ ] finish otel file exporter
// - [ ] report resource consumption for server process
// - [ ] make request buffer thread-safe?
// - [ ] better log file management

type lspwatchInstance struct {
	cfg             internal.LspwatchConfig
	logger          *logrus.Logger
	logFile         *os.File
	requestsHandler *internal.RequestsHandler
	serverCmd       *exec.Cmd
	stdoutPipe      io.ReadCloser
	stdinPipe       io.WriteCloser
}

var cfgFilePath string
var rootCmd = &cobra.Command{
	Use:   "lspwatch",
	Short: "lspwatch provides observability for LSP-compliant language servers over stdin/stdout",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var serverArgs []string
		if len(args) > 1 {
			serverArgs = args[1:]
		} else {
			serverArgs = []string{}
		}

		lspwatchInstance, err := setUpLspwatch(args[0], serverArgs)

		if err != nil {
			fmt.Printf("error setting up lspwatch: %v\n", err)
			os.Exit(1)
		}

		lspwatchInstance.runProxy()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFilePath, "config", "", "path to config file for lspwatch")
}

func setUpLspwatch(serverShellCommand string, args []string) (lspwatchInstance, error) {
	logger, logFile, err := internal.CreateLogger("lspwatch.log", true)
	if err != nil {
		return lspwatchInstance{}, fmt.Errorf("error creating logger: %v", err)
	}

	cfg, err := getConfig(cfgFilePath)
	if err != nil {
		return lspwatchInstance{}, fmt.Errorf("error getting lspwatch config: %v", err)
	}

	// TODO: Make pointer?
	if cfg.EnvFilePath != "" {
		err = godotenv.Load(cfg.EnvFilePath)
		if err != nil {
			logger.Fatalf("error loading .env file: %v", err)
		}
	}

	requestsHandler, err := internal.NewRequestsHandler(&cfg, logger)
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

	return lspwatchInstance{
		cfg:             cfg,
		logger:          logger,
		logFile:         logFile,
		requestsHandler: requestsHandler,
		serverCmd:       serverCmd,
		stdoutPipe:      stdoutPipe,
		stdinPipe:       stdinPipe,
	}, nil
}

func getConfig(path string) (internal.LspwatchConfig, error) {
	if path == "" {
		return internal.GetDefaultConfig(), nil
	}

	return internal.ReadLspwatchConfig(path)
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

func launchProcessExitListener(serverCmd *exec.Cmd, outgoingStopSignal chan error) {
	go func() {
		err := serverCmd.Wait()
		outgoingStopSignal <- err
	}()
}

func (lspwatchInstance *lspwatchInstance) runProxy() {
	logger := lspwatchInstance.logger
	logFile := lspwatchInstance.logFile
	serverCmd := lspwatchInstance.serverCmd
	requestsHandler := lspwatchInstance.requestsHandler

	// TODO: Take args[1:]
	logger.Infof("starting language server using command '%v' and args '%v'", serverCmd.Path, serverCmd.Args)

	err := serverCmd.Start()
	if err != nil {
		logger.Fatalf("error starting language server process: %v", err)
	}

	logger.Infof("launched language server process (PID=%v)", serverCmd.Process.Pid)

	launchInterruptListener(serverCmd, logger)

	processExitChan := make(chan error)
	launchProcessExitListener(serverCmd, processExitChan)

	var listenersWaitGroup sync.WaitGroup
	listenersWaitGroup.Add(2)
	listenersStopChan := make(chan struct{})
	go requestsHandler.ListenServer(lspwatchInstance.stdoutPipe, listenersStopChan, &listenersWaitGroup, logger)
	go requestsHandler.ListenClient(lspwatchInstance.stdinPipe, listenersStopChan, &listenersWaitGroup, logger)

	exitCode := 0

	defer func() {
		// Wait for all server and client listeners to exit
		listenersWaitGroup.Wait()
		err := requestsHandler.Exporter.Shutdown()
		if err != nil {
			logger.Errorf("error shutting down metrics exporter: %v", err)
		} else {
			logger.Info("metrics exporter shutdown complete")
		}

		lspwatchInstance.stdoutPipe.Close()
		lspwatchInstance.stdinPipe.Close()

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
