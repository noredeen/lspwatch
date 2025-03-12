package internal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/core"
	"github.com/noredeen/lspwatch/internal/exporters"
	lspwatch_io "github.com/noredeen/lspwatch/internal/io"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
)

type LspwatchInstance struct {
	cfg            config.LspwatchConfig
	exporter       telemetry.MetricsExporter
	proxyHandler   *core.ProxyHandler
	processWatcher *core.ProcessWatcher
	logger         *logrus.Logger
	logFile        *os.File
	serverCmd      *exec.Cmd
	serverStderr   io.ReadCloser
}

var availableLSPMetrics = map[telemetry.AvailableMetric]telemetry.MetricRegistration{
	telemetry.RequestDuration: {
		Kind:        telemetry.Histogram,
		Name:        "lspwatch.request.duration",
		Description: "Duration of LSP request",
		Unit:        "s",
	},
}

var availableServerMetrics = map[telemetry.AvailableMetric]telemetry.MetricRegistration{
	telemetry.ServerRSS: {
		Kind:        telemetry.Histogram,
		Name:        "lspwatch.server.rss",
		Description: "RSS of the language server process",
		Unit:        "bytes", // TODO: Check if this is correct
	},
}

func (lspwatchInstance *LspwatchInstance) Release() error {
	err := lspwatchInstance.logFile.Close()
	if err != nil {
		return fmt.Errorf("error closing log file: %v", err)
	}

	return nil
}

// Synchronously starts the language server and all associated components.
// Returns only when all components in lspwatch have shut down.
func (lspwatchInstance *LspwatchInstance) Run() error {
	logger := lspwatchInstance.logger
	serverCmd := lspwatchInstance.serverCmd
	exporter := lspwatchInstance.exporter
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	logger.Infof("starting language server using command '%v' and args '%v'", serverCmd.Path, serverCmd.Args[1:])
	err := serverCmd.Start()
	if err != nil {
		return fmt.Errorf("error starting language server process: %v", err)
	}

	logger.Infof("launched language server process (PID=%v)", serverCmd.Process.Pid)

	processHandle := serverCmd.Process
	processInfo, err := process.NewProcess(int32(processHandle.Pid))
	if err != nil {
		return fmt.Errorf("error creating process info provider: %v", err)
	}

	exporter.Start()
	proxyHandler.Start()
	processWatcher.Start(processHandle, processInfo)
	lspwatchInstance.startInterruptListener()
	lspwatchInstance.startStderrRecorder()

	exitCode := 0
	defer func() {
		lspwatchInstance.shutdownAndWait()
		os.Exit(exitCode)
	}()

	// Create a timer in a stopped state (already expired)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}

	for {
		select {
		case state := <-processWatcher.ProcessExited():
			{
				if state != nil {
					// TODO: Try to get the real code for signal exits?
					exitCode = state.ExitCode()
					logger.Infof("language server process exited with code %d", exitCode)
				} else {
					exitCode = 1
				}

				return nil
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
			logger.Info("organic language server shutdown failed. forcing with SIGKILL...")
			err := serverCmd.Process.Signal(syscall.SIGKILL)
			if err != nil {
				logger.Fatalf("error signaling language server to shut down: %v", err)
			}
		}
	}
}

// Unlike public Shutdown() methods in lspwatch, this will block and wait for
// all shutdowns to complete before exiting.
func (lspwatchInstance *LspwatchInstance) shutdownAndWait() {
	logger := lspwatchInstance.logger
	exporter := lspwatchInstance.exporter
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	err := proxyHandler.Shutdown()
	if err != nil {
		logger.Fatalf("error shutting down proxy handler: %v", err)
	}

	err = processWatcher.Shutdown()
	if err != nil {
		logger.Fatalf("error shutting down process watcher: %v", err)
	}

	proxyHandler.Wait()
	processWatcher.Wait()

	// Shut down exporter only after proxy handler and process watcher have
	// flushed their metrics and exited.
	err = exporter.Shutdown()
	if err != nil {
		logger.Fatalf("error shutting down metrics exporter: %v", err)
	}

	exporter.Wait()
	logger.Info("metrics exporter shutdown complete")
	logger.Info("lspwatch shutdown complete. goodbye!")

	err = exporter.Release()
	if err != nil {
		logger.Errorf("error releasing metrics exporter: %v", err)
	}

	err = lspwatchInstance.Release()
	if err != nil {
		logger.Errorf("error releasing lspwatch resources: %v", err)
	}
}

// Creates a new LspwatchInstance. Does NOT start the language server process.
func NewLspwatchInstance(
	serverShellCommand string,
	args []string,
	configFilePath string,
	logDir string,
	mode string,
) (LspwatchInstance, error) {
	logger, logFile, err := lspwatch_io.CreateLogger(logDir, "lspwatch.log")
	if err != nil {
		msg := fmt.Sprintf("error creating logger: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	cfg, err := getConfig(configFilePath)
	if err != nil {
		msg := fmt.Sprintf("error getting lspwatch config: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	if cfg.EnvFilePath != "" {
		err = godotenv.Load(cfg.EnvFilePath)
		if err != nil {
			msg := fmt.Sprintf("error loading .env file: %v", err)
			logger.Error(msg)
			return LspwatchInstance{}, errors.New(msg)
		}
	}

	serverCmd := exec.Command(serverShellCommand, args...)
	errPipe, err := serverCmd.StderrPipe()
	if err != nil {
		msg := fmt.Sprintf("error creating pipe to server's stderr: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	serverStdoutPipe, err := serverCmd.StdoutPipe()
	if err != nil {
		msg := fmt.Sprintf("error creating pipe to server's stdout: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	serverStdinPipe, err := serverCmd.StdinPipe()
	if err != nil {
		msg := fmt.Sprintf("error creating pipe to server's stdin: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	exporter, err := newMetricsExporter(cfg, logDir)
	if err != nil {
		msg := fmt.Sprintf("error creating metrics exporter: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	tagGetters := map[telemetry.AvailableTag]func() telemetry.TagValue{
		telemetry.OS: func() telemetry.TagValue {
			return telemetry.TagValue(runtime.GOOS)
		},
		telemetry.LanguageServer: func() telemetry.TagValue {
			// TODO: This is not robust.
			return telemetry.TagValue(filepath.Base(serverCmd.Path))
		},
		telemetry.RAM: func() telemetry.TagValue {
			vmem, err := mem.VirtualMemory()
			if err != nil {
				logger.Errorf("error getting total system memory: %v", err)
				return ""
			}
			totalGB := vmem.Total / (1024 * 1024 * 1024)
			return telemetry.TagValue(fmt.Sprintf("%v", totalGB))
		},
		telemetry.User: func() telemetry.TagValue {
			curr, err := user.Current()
			if err != nil {
				logger.Errorf("error getting current user: %v", err)
				return ""
			}
			return telemetry.TagValue(curr.Username)
		},
	}

	tags, err := getTagValues(&cfg, tagGetters)
	if err != nil {
		msg := fmt.Sprintf("error getting tag values: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}
	exporter.SetGlobalTags(tags...)

	requestMetricsRegistry := telemetry.NewDefaultMetricsRegistry(exporter, availableLSPMetrics)
	proxyHandler, err := core.NewProxyHandler(
		&cfg,
		&requestMetricsRegistry,
		os.Stdin,
		os.Stdout,
		serverStdoutPipe,
		serverStdinPipe,
		mode,
		logger,
	)
	if err != nil {
		msg := fmt.Sprintf("error initializing LSP request handler: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	serverMetricsRegistry := telemetry.NewDefaultMetricsRegistry(exporter, availableServerMetrics)
	processWatcher, err := core.NewProcessWatcher(
		&serverMetricsRegistry,
		&cfg,
		logger,
	)
	if err != nil {
		msg := fmt.Sprintf("error initializing process watcher: %v", err)
		logger.Error(msg)
		return LspwatchInstance{}, errors.New(msg)
	}

	return LspwatchInstance{
		cfg:            cfg,
		exporter:       exporter,
		logger:         logger,
		logFile:        logFile,
		proxyHandler:   proxyHandler,
		processWatcher: processWatcher,
		serverCmd:      serverCmd,
		serverStderr:   errPipe,
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

func (lspwatch *LspwatchInstance) startInterruptListener() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)

	go func() {
		for sig := range signalChan {
			lspwatch.logger.Infof("lspwatch process interrupted. forwarding signal to language server...")
			err := lspwatch.serverCmd.Process.Signal(sig)
			if err != nil {
				lspwatch.logger.Fatalf(
					"error forwarding signal to language server process (PID=%v): %v",
					lspwatch.serverCmd.Process.Pid,
					err,
				)
			}
		}
	}()
}

// TODO: Test this functionality in integration tests.
func (lspwatch *LspwatchInstance) startStderrRecorder() {
	go func() {
		var stderrBuf bytes.Buffer
		_, err := io.Copy(io.MultiWriter(&stderrBuf, os.Stderr), lspwatch.serverStderr)
		if err != nil {
			lspwatch.logger.Errorf("error copying stderr: %v", err)
		}
		if stderrBuf.Len() > 0 {
			lspwatch.logger.Infof("captured stderr output:\n%s", stderrBuf.String())
		}
	}()
}

func getTagValues(
	cfg *config.LspwatchConfig,
	tagGetters map[telemetry.AvailableTag]func() telemetry.TagValue,
) ([]telemetry.Tag, error) {
	tags := make([]telemetry.Tag, 0, len(cfg.Tags))
	for _, tag := range cfg.Tags {
		tagGetter, ok := tagGetters[telemetry.AvailableTag(tag)]
		if !ok {
			return []telemetry.Tag{}, fmt.Errorf("tag '%v' not supported", tag)
		}

		tags = append(tags, telemetry.NewTag(tag, tagGetter()))
	}

	return tags, nil
}

func newMetricsExporter(
	cfg config.LspwatchConfig,
	logDir string,
) (telemetry.MetricsExporter, error) {
	var exporter telemetry.MetricsExporter

	switch cfg.Exporter {
	case "opentelemetry":
		otelExporter, err := exporters.NewMetricsOTelExporter(cfg.OpenTelemetry, logDir)
		if err != nil {
			return nil, fmt.Errorf("error creating OpenTelemetry exporter: %v", err)
		}
		exporter = otelExporter
	case "datadog":
		datadogCtx := exporters.GetDatadogContext(cfg.Datadog)
		datadogExporter, err := exporters.NewDatadogMetricsExporter(
			datadogCtx,
			cfg.Datadog,
			logDir,
		)
		if err != nil {
			return nil, fmt.Errorf("error creating Datadog exporter: %v", err)
		}
		exporter = datadogExporter
	default:
		return nil, fmt.Errorf("invalid exporter: %v", cfg.Exporter)
	}

	return exporter, nil
}
