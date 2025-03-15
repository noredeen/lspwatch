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
}

type ProcessInfo interface {
	MemoryInfo() (*process.MemoryInfoStat, error)
}

type ProcessWatcher struct {
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

func (pw *ProcessWatcher) Start(processHandle ProcessHandle, processInfo ProcessInfo) error {
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
					memoryInfo, err := processInfo.MemoryInfo()
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
	pw.shutdownOnce.Do(func() {
		close(pw.incomingShutdown)
	})

	return nil
}

func (pw *ProcessWatcher) Wait() {
	pw.wg.Wait()
	pw.logger.Info("process watcher monitors shutdown complete")
}

func (pw *ProcessWatcher) ProcessExited() chan *os.ProcessState {
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
	metricsRegistry telemetry.MetricsRegistry,
	cfg *config.LspwatchConfig,
	logger *logrus.Logger,
) (*ProcessWatcher, error) {
	pollingInterval := 5 * time.Second
	if cfg.PollingInterval != nil {
		pollingInterval = time.Duration(*cfg.PollingInterval) * time.Second
	}

	pw := ProcessWatcher{
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
