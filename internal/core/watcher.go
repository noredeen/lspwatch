package core

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
)

type ProcessHandle interface {
	Wait() (*os.ProcessState, error)
	MemoryInfo() (*process.MemoryInfoStat, error)
}

type DefaultProcessHandle struct {
	proc     *os.Process
	procInfo *process.Process
}

func (ph *DefaultProcessHandle) Wait() (*os.ProcessState, error) {
	return ph.proc.Wait()
}

func (ph *DefaultProcessHandle) MemoryInfo() (*process.MemoryInfoStat, error) {
	return ph.procInfo.MemoryInfo()
}

type ServerWatcher struct {
	metricsRegistry telemetry.MetricsRegistry
	pollingInterval time.Duration
	processExited   bool

	processExitedChan chan *os.ProcessState
	incomingShutdown  chan struct{}
	shutdownOnce      sync.Once
	logger            *logrus.Logger
	mu                sync.Mutex
	wg                *sync.WaitGroup
}

func (pw *ServerWatcher) Start(processHandle ProcessHandle) {
	// I'm ok with letting this goroutine run indefinitely (for now).
	go func() {
		state, err := processHandle.Wait()
		if err != nil {
			pw.logger.Errorf("fatal error waiting for process to exit: %v", err)
			os.Exit(1)
		}

		pw.mu.Lock()
		pw.processExited = true
		pw.processExitedChan <- state
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
					memoryInfo, err := processHandle.MemoryInfo()
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

	pw.logger.Info("server watcher started")
}

// Idempotent and non-blocking. Use Wait() to block until shutdown is complete.
func (pw *ServerWatcher) Shutdown() error {
	pw.shutdownOnce.Do(func() {
		close(pw.incomingShutdown)
	})

	return nil
}

func (pw *ServerWatcher) Wait() {
	pw.wg.Wait()
	pw.logger.Info("server watcher monitors shutdown complete")
}

func (pw *ServerWatcher) ProcessExited() chan *os.ProcessState {
	return pw.processExitedChan
}

func (pw *ServerWatcher) enableMetrics(cfg *config.LspwatchConfig) error {
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

// Returns an error if the process is not running.
func NewDefaultProcessHandle(proc *os.Process) (*DefaultProcessHandle, error) {
	procInfo, err := process.NewProcess(int32(proc.Pid))
	if err != nil {
		return nil, err
	}

	return &DefaultProcessHandle{proc: proc, procInfo: procInfo}, nil
}

func NewServerWatcher(
	metricsRegistry telemetry.MetricsRegistry,
	cfg *config.LspwatchConfig,
	logger *logrus.Logger,
) (*ServerWatcher, error) {
	pollingInterval := 5 * time.Second
	if cfg.PollingInterval != nil {
		pollingInterval = time.Duration(*cfg.PollingInterval) * time.Second
	}

	pw := ServerWatcher{
		metricsRegistry:   metricsRegistry,
		pollingInterval:   pollingInterval,
		processExitedChan: make(chan *os.ProcessState),
		incomingShutdown:  make(chan struct{}),
		shutdownOnce:      sync.Once{},
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
