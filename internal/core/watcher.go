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

type ProcessWatcher struct {
	metricsRegistry   telemetry.MetricsRegistry
	process           *os.Process
	processExitedChan chan error
	processExited     bool
	incomingShutdown  chan struct{}
	logger            *logrus.Logger
	mu                sync.Mutex
	wg                *sync.WaitGroup
}

var availableServerMetrics = map[telemetry.AvailableMetric]telemetry.MetricRegistration{
	telemetry.ServerRSS: {
		Kind:        telemetry.Histogram,
		Name:        "lspwatch.server.rss",
		Description: "RSS of the language server process",
		Unit:        "bytes", // TODO: Check if this is correct
	},
}

func (pw *ProcessWatcher) Launch() error {
	utilProc, err := process.NewProcess(int32(pw.process.Pid))
	if err != nil {
		return err
	}

	// I'm ok with letting this goroutine run indefinitely (for now)
	go func() {
		// TODO: use the returned state value
		_, err := pw.process.Wait()
		pw.mu.Lock()
		if pw.processExitedChan != nil {
			pw.processExited = true
			pw.processExitedChan <- err
			pw.processExitedChan = nil
		}
		pw.mu.Unlock()
	}()

	ticker := time.NewTicker(4 * time.Second)
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
					memoryInfo, err := utilProc.MemoryInfo()
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

func (pw *ProcessWatcher) ProcessExited() chan error {
	return pw.processExitedChan
}

// TODO: Move to MetricsRegistry
func (pw *ProcessWatcher) registerMetrics(cfg *config.LspwatchConfig) error {
	// Default behavior if `metrics` is not specified in the config
	if cfg.Metrics == nil {
		err := pw.metricsRegistry.RegisterMetric(telemetry.ServerRSS)
		if err != nil {
			return err
		}
		return nil
	}

	for _, metric := range *cfg.Metrics {
		err := pw.metricsRegistry.RegisterMetric(telemetry.AvailableMetric(metric))
		if err != nil {
			return err
		}
	}

	return nil
}

func NewProcessWatcher(process *os.Process, exporter telemetry.MetricsExporter, cfg *config.LspwatchConfig, logger *logrus.Logger) (*ProcessWatcher, error) {
	pw := ProcessWatcher{
		metricsRegistry:   telemetry.NewMetricsRegistry(exporter, availableServerMetrics),
		process:           process,
		processExitedChan: make(chan error),
		incomingShutdown:  make(chan struct{}),
		mu:                sync.Mutex{},
		logger:            logger,
		wg:                &sync.WaitGroup{},
	}

	err := pw.registerMetrics(cfg)
	if err != nil {
		return nil, fmt.Errorf("error registering metrics: %v", err)
	}

	return &pw, nil
}
