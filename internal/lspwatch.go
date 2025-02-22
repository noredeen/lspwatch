package internal

import (
	"fmt"
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
	var errors []error
	err := lspwatchInstance.logFile.Close()
	if err != nil {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		return fmt.Errorf("issues: %v", errors)
	}

	return nil
}

// Synchronously starts the language server and all associated components.
// Returns only when all components in lspwatch have shut down.
func (lspwatchInstance *LspwatchInstance) Run() {
	logger := lspwatchInstance.logger
	serverCmd := lspwatchInstance.serverCmd
	exporter := lspwatchInstance.exporter
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	startInterruptListener(serverCmd, logger)
	exporter.Start()
	proxyHandler.Start()
	processWatcher.Start()

	exitCode := 0

	defer func() {
		err := lspwatchInstance.shutdownAndWait()
		if err != nil {
			logger.Fatalf("error shutting down lspwatch instance: %v", err)
		}

		os.Exit(exitCode)
	}()

	// Create a timer in a stopped state (already expired)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}

	for {
		select {
		case err := <-processWatcher.ProcessExited():
			{
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
func (lspwatchInstance *LspwatchInstance) shutdownAndWait() error {
	logger := lspwatchInstance.logger
	exporter := lspwatchInstance.exporter
	proxyHandler := lspwatchInstance.proxyHandler
	processWatcher := lspwatchInstance.processWatcher

	err := proxyHandler.Shutdown()
	if err != nil {
		return fmt.Errorf("error shutting down proxy handler: %v", err)
	}

	err = processWatcher.Shutdown()
	if err != nil {
		return fmt.Errorf("error shutting down process watcher: %v", err)
	}

	proxyHandler.Wait()
	processWatcher.Wait()

	// Shut down exporter only after proxy handler and process watcher have
	// flushed their metrics and exited.
	err = exporter.Shutdown()
	if err != nil {
		return fmt.Errorf("error shutting down exporter: %v", err)
	}

	exporter.Wait()
	logger.Info("metrics exporter shutdown complete")
	logger.Info("lspwatch shutdown complete. goodbye!")

	err = lspwatchInstance.Release()
	if err != nil {
		return fmt.Errorf("error releasing lspwatch resources: %v", err)
	}

	return nil
}

// NOTE: This starts the language server process. Proxying and monitoring facilities
// are not started until Run() is called.
func NewLspwatchInstance(
	serverShellCommand string,
	args []string,
	cfgFilePath string,
	enableLogging bool,
) (LspwatchInstance, error) {
	logger, logFile, err := lspwatch_io.CreateLogger("lspwatch.log", enableLogging)
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

	serverStdoutPipe, err := serverCmd.StdoutPipe()
	if err != nil {
		logger.Fatalf("error creating pipe to server's stdout: %v", err)
	}

	serverStdinPipe, err := serverCmd.StdinPipe()
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

	exporter, err := newMetricsExporter(cfg, enableLogging)
	if err != nil {
		logger.Fatalf("error creating metrics exporter: %v", err)
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
		logger.Fatalf("error getting tag values: %v", err)
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
		logger,
	)
	if err != nil {
		logger.Fatalf("error initializing LSP request handler: %v", err)
	}

	processHandle := serverCmd.Process
	processInfo, err := process.NewProcess(int32(processHandle.Pid))
	if err != nil {
		logger.Fatalf("error creating process info: %v", err)
	}

	serverMetricsRegistry := telemetry.NewDefaultMetricsRegistry(exporter, availableServerMetrics)
	processWatcher, err := core.NewProcessWatcher(
		processHandle,
		processInfo,
		&serverMetricsRegistry,
		&cfg,
		logger,
	)
	if err != nil {
		logger.Fatalf("error initializing process watcher: %v", err)
	}

	return LspwatchInstance{
		cfg:            cfg,
		exporter:       exporter,
		logger:         logger,
		logFile:        logFile,
		proxyHandler:   proxyHandler,
		processWatcher: processWatcher,
		serverCmd:      serverCmd,
	}, nil
}

// TODO: Test
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

func startInterruptListener(serverCmd *exec.Cmd, logger *logrus.Logger) {
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

// TODO: Test
func newMetricsExporter(
	cfg config.LspwatchConfig,
	enableLogging bool,
) (telemetry.MetricsExporter, error) {
	var exporter telemetry.MetricsExporter

	switch cfg.Exporter {
	case "opentelemetry":
		otelExporter, err := exporters.NewMetricsOTelExporter(cfg.OpenTelemetry, enableLogging)
		if err != nil {
			return nil, fmt.Errorf("error creating OpenTelemetry exporter: %v", err)
		}
		exporter = otelExporter
	case "datadog":
		datadogCtx := exporters.GetDatadogContext(cfg.Datadog)
		datadogExporter, err := exporters.NewDatadogMetricsExporter(
			datadogCtx,
			cfg.Datadog,
			enableLogging,
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
