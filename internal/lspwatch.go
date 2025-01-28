package internal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/core"
	"github.com/noredeen/lspwatch/internal/exporters"
	lspwatch_io "github.com/noredeen/lspwatch/internal/io"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/sirupsen/logrus"
)

type LspwatchInstance struct {
	cfg            config.LspwatchConfig
	proxyHandler   *core.ProxyHandler
	processWatcher *core.ProcessWatcher
	logger         *logrus.Logger
	logFile        *os.File
	serverCmd      *exec.Cmd
	stdoutPipe     io.ReadCloser
	stdinPipe      io.WriteCloser
}

func (lspwatchInstance *LspwatchInstance) Release() {
	lspwatchInstance.stdoutPipe.Close()
	lspwatchInstance.stdinPipe.Close()
}

func (lspwatchInstance *LspwatchInstance) Run() {
	logger := lspwatchInstance.logger
	serverCmd := lspwatchInstance.serverCmd
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	launchInterruptListener(serverCmd, logger)
	proxyHandler.Launch(lspwatchInstance.stdoutPipe, lspwatchInstance.stdinPipe)
	processWatcher.Launch()

	exitCode := 0

	defer func() { lspwatchInstance.Shutdown(exitCode) }()

	// Create a timer in a stopped state (already expired)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}

	for {
		select {
		case err := <-processWatcher.ProcessExited():
			{
				logger.Info("language server process exited")

				if err != nil {
					exitCode = serverCmd.ProcessState.ExitCode()
				}

				return
			}
		case <-proxyHandler.ShutdownRequested():
			// One of the proxy listeners requested exit
			{
				// Confirm the shutdown and propagate to language server
				err := proxyHandler.Shutdown()
				if err != nil {
					logger.Errorf("error shutting down proxy handler: %v", err)
				}

				// Start timer to wait for language server to exit
				timer.Reset(3 * time.Second)
			}
		case <-timer.C:
			logger.Info("organic language server shutdown failed. forcing with a signal...")
			err := serverCmd.Process.Signal(syscall.SIGINT)
			if err != nil {
				logger.Fatalf("error signaling language server to shut down: %v", err)
			}
		}
	}
}

func (lspwatchInstance *LspwatchInstance) Shutdown(exitCode int) {
	logger := lspwatchInstance.logger
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	err := proxyHandler.Shutdown()
	if err != nil {
		logger.Errorf("error shutting down proxy handler: %v", err)
	}

	err = processWatcher.Shutdown()
	if err != nil {
		logger.Errorf("error shutting down process watcher: %v", err)
	}

	proxyHandler.Wait()
	processWatcher.Wait()
	lspwatchInstance.Release()
	logger.Info("lspwatch shutdown complete. goodbye!")

	err = lspwatchInstance.logFile.Close()
	if err != nil {
		logger.Errorf("error closing log file: %v", err)
	}

	os.Exit(exitCode)
}

func NewLspwatchInstance(serverShellCommand string, args []string, cfgFilePath string) (LspwatchInstance, error) {
	logger, logFile, err := lspwatch_io.CreateLogger("lspwatch.log", true)
	if err != nil {
		return LspwatchInstance{}, fmt.Errorf("error creating logger: %v", err)
	}

	cfg, err := getConfig(cfgFilePath)
	if err != nil {
		return LspwatchInstance{}, fmt.Errorf("error getting lspwatch config: %v", err)
	}

	if cfg.EnvFilePath != "" {
		err = godotenv.Load(cfg.EnvFilePath)
		if err != nil {
			logger.Fatalf("error loading .env file: %v", err)
		}
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

	// TODO: Take args[1:]
	logger.Infof("starting language server using command '%v' and args '%v'", serverCmd.Path, serverCmd.Args)
	err = serverCmd.Start()
	if err != nil {
		logger.Fatalf("error starting language server process: %v", err)
	}

	logger.Infof("launched language server process (PID=%v)", serverCmd.Process.Pid)

	exporter, err := newMetricsExporter(cfg, logger)
	if err != nil {
		logger.Fatalf("error creating metrics exporter: %v", err)
	}

	proxyHandler, err := core.NewProxyHandler(exporter, &cfg, logger)
	if err != nil {
		logger.Fatalf("error initializing LSP request handler: %v", err)
	}

	processWatcher, err := core.NewProcessWatcher(serverCmd.Process, exporter, &cfg, logger)
	if err != nil {
		logger.Fatalf("error initializing process watcher: %v", err)
	}

	return LspwatchInstance{
		cfg:            cfg,
		logger:         logger,
		logFile:        logFile,
		proxyHandler:   proxyHandler,
		processWatcher: processWatcher,
		serverCmd:      serverCmd,
		stdoutPipe:     stdoutPipe,
		stdinPipe:      stdinPipe,
	}, nil
}

func getConfig(path string) (config.LspwatchConfig, error) {
	if path == "" {
		return config.GetDefaultConfig(), nil
	}

	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return config.LspwatchConfig{}, fmt.Errorf("error loading config file: %v", err)
	}

	cfg, err := config.ReadLspwatchConfig(fileBytes)
	if err != nil {
		return config.LspwatchConfig{}, fmt.Errorf("error parsing lspwatch config: %v", err)
	}

	return cfg, nil
}

func launchInterruptListener(serverCmd *exec.Cmd, logger *logrus.Logger) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)

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

func newMetricsExporter(cfg config.LspwatchConfig, logger *logrus.Logger) (telemetry.MetricsExporter, error) {
	var exporter telemetry.MetricsExporter

	switch cfg.Exporter {
	case "opentelemetry":
		otelExporter, err := exporters.NewMetricsOTelExporter(cfg.OpenTelemetry, logger)
		if err != nil {
			return nil, fmt.Errorf("error creating OpenTelemetry exporter: %v", err)
		}
		exporter = otelExporter
	case "datadog":
		datadogExporter, err := exporters.NewDatadogMetricsExporter(cfg.Datadog)
		if err != nil {
			return nil, fmt.Errorf("error creating Datadog exporter: %v", err)
		}
		exporter = datadogExporter
	default:
		return nil, fmt.Errorf("invalid exporter: %v", cfg.Exporter)
	}

	return exporter, nil
}
