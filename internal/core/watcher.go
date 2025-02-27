package core

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
)

type CommandProcess interface {
	Signal(sig os.Signal) error
	Wait() (*os.ProcessState, error)
	Start() error
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	ProcessPid() int
	ProcessMemoryInfo() (*process.MemoryInfoStat, error)
	CommandPath() string
	CommandArgs() []string
}

type ProcessHandle interface {
	Wait() (*os.ProcessState, error)
}

type ProcessInfo interface {
	MemoryInfo() (*process.MemoryInfoStat, error)
}

type DefaultCommandProcess struct {
	serverCmd   *exec.Cmd
	processInfo *process.Process
}

var _ CommandProcess = &DefaultCommandProcess{}

type ProcessWatcher struct {
	processCmd        CommandProcess
	metricsRegistry   telemetry.MetricsRegistry
	pollingInterval   time.Duration
	processExited     bool
	processExitedChan chan int
	incomingShutdown  chan struct{}
	logger            *logrus.Logger
	mu                sync.Mutex
	wg                *sync.WaitGroup
}

func (dcp *DefaultCommandProcess) Signal(sig os.Signal) error {
	return dcp.serverCmd.Process.Signal(sig)
}

func (dcp *DefaultCommandProcess) Wait() (*os.ProcessState, error) {
	return dcp.serverCmd.Process.Wait()
}

func (dcp *DefaultCommandProcess) Start() error {
	err := dcp.serverCmd.Start()
	if err != nil {
		return err
	}

	dcp.processInfo, err = process.NewProcess(int32(dcp.serverCmd.Process.Pid))
	if err != nil {
		return err
	}

	return nil
}

func (dcp *DefaultCommandProcess) StdinPipe() (io.WriteCloser, error) {
	return dcp.serverCmd.StdinPipe()
}

func (dcp *DefaultCommandProcess) StdoutPipe() (io.ReadCloser, error) {
	return dcp.serverCmd.StdoutPipe()
}

func (dcp *DefaultCommandProcess) ProcessPid() int {
	return dcp.serverCmd.Process.Pid
}

func (dcp *DefaultCommandProcess) ProcessMemoryInfo() (*process.MemoryInfoStat, error) {
	return dcp.processInfo.MemoryInfo()
}

func (dcp *DefaultCommandProcess) CommandPath() string {
	return dcp.serverCmd.Path
}

func (dcp *DefaultCommandProcess) CommandArgs() []string {
	return dcp.serverCmd.Args
}

func (pw *ProcessWatcher) Start() error {
	// I'm ok with letting this goroutine run indefinitely (for now)
	go func() {
		state, err := pw.processCmd.Wait()
		if err != nil {
			pw.logger.Errorf("error waiting for process to exit: %v", err)
		}

		var exitCode int
		if state != nil {
			if ws, ok := state.Sys().(syscall.WaitStatus); ok {
				if ws.Signaled() {
					// Process was terminated by a signal.
					exitCode = 128 + int(ws.Signal())
				} else {
					// Normal exit.
					exitCode = ws.ExitStatus()
				}
			} else {
				// Fallback to basic ExitCode() if WaitStatus isn't available.
				exitCode = state.ExitCode()
			}
		} else {
			// If state is nil, something went wrong.
			exitCode = -1
		}

		pw.mu.Lock()
		pw.processExited = true
		pw.processExitedChan <- exitCode
		pw.mu.Unlock()
	}()

	ticker := time.NewTicker(pw.pollingInterval)
	pw.wg.Add(1)
	go func() {
		defer func() {
			ticker.Stop()
			pw.wg.Done()
		}()

		for {
			select {
			case <-pw.incomingShutdown:
				return
			case <-ticker.C:
				pw.mu.Lock()
				if pw.processExited {
					pw.mu.Unlock()
					return
				}
				pw.mu.Unlock()

				if pw.metricsRegistry.IsMetricEnabled(telemetry.ServerRSS) {
					memoryInfo, err := pw.processCmd.ProcessMemoryInfo()
					if err != nil {
						pw.logger.Errorf("failed to get memory info: %v", err)
						continue
					}

					metric := telemetry.NewMetricRecording(
						telemetry.ServerRSS,
						time.Now().Unix(),
						float64(memoryInfo.RSS),
					)

					err = pw.metricsRegistry.EmitMetric(metric)
					if err != nil {
						pw.logger.Errorf("failed to emit metric: %v", err)
					}
				}
			}
		}
	}()

	pw.logger.Info("process watcher started")
	return nil
}

// Idempotent and non-blocking. Use Wait() to block until shutdown is complete.
func (pw *ProcessWatcher) Shutdown() error {
	if pw.incomingShutdown != nil {
		close(pw.incomingShutdown)
		pw.incomingShutdown = nil
	}

	return nil
}

func (pw *ProcessWatcher) Wait() {
	pw.wg.Wait()
	pw.logger.Info("process watcher monitors shutdown complete")
}

func (pw *ProcessWatcher) ProcessExited() chan int {
	return pw.processExitedChan
}

func (pw *ProcessWatcher) enableMetrics(cfg *config.LspwatchConfig) error {
	// Default behavior if `metrics` is not specified in the config
	if cfg.Metrics == nil {
		err := pw.metricsRegistry.EnableMetric(telemetry.ServerRSS)
		if err != nil {
			return err
		}
		return nil
	}

	for _, metric := range *cfg.Metrics {
		err := pw.metricsRegistry.EnableMetric(telemetry.AvailableMetric(metric))
		if err != nil {
			return err
		}
	}

	return nil
}

func NewProcessWatcher(
	processCmd CommandProcess,
	// processHandle ProcessHandle,
	// processInfo ProcessInfo,
	metricsRegistry telemetry.MetricsRegistry,
	cfg *config.LspwatchConfig,
	logger *logrus.Logger,
) (*ProcessWatcher, error) {
	pollingInterval := 5 * time.Second
	if cfg.PollingInterval != nil {
		pollingInterval = time.Duration(*cfg.PollingInterval) * time.Second
	}

	pw := ProcessWatcher{
		processCmd: processCmd,
		// processHandle:     processHandle,
		// processInfo:       processInfo,
		metricsRegistry:   metricsRegistry,
		pollingInterval:   pollingInterval,
		processExitedChan: make(chan int),
		incomingShutdown:  make(chan struct{}),
		mu:                sync.Mutex{},
		logger:            logger,
		wg:                &sync.WaitGroup{},
	}

	err := pw.enableMetrics(cfg)
	if err != nil {
		return nil, fmt.Errorf("error enabling metrics: %v", err)
	}

	return &pw, nil
}

func NewDefaultCommandProcess(command string, args ...string) CommandProcess {
	serverCmd := exec.Command(command, args...)
	return &DefaultCommandProcess{
		serverCmd: serverCmd,
	}
}
