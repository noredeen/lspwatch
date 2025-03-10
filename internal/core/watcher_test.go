package core

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/noredeen/lspwatch/internal/config"
	"github.com/noredeen/lspwatch/internal/telemetry"
	"github.com/noredeen/lspwatch/internal/testutil"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
)

type mockWatcherMetricsRegistry struct {
	enableMetricCalls []telemetry.AvailableMetric
	emitMetricCalls   []telemetry.MetricRecording
	mu                sync.Mutex
}

type mockProcessHandle struct {
	done chan struct{}
}

type mockProcessInfo struct{}

var _ telemetry.MetricsRegistry = &mockWatcherMetricsRegistry{}
var _ ProcessHandle = &mockProcessHandle{}
var _ ProcessInfo = &mockProcessInfo{}

// TODO all mocks
func (m *mockWatcherMetricsRegistry) EnableMetric(metric telemetry.AvailableMetric) error {
	m.enableMetricCalls = append(m.enableMetricCalls, metric)
	return nil
}

func (m *mockWatcherMetricsRegistry) EmitMetric(metric telemetry.MetricRecording) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitMetricCalls = append(m.emitMetricCalls, metric)
	return nil
}

func (m *mockWatcherMetricsRegistry) IsMetricEnabled(metric telemetry.AvailableMetric) bool {
	return true
}

func (m *mockProcessHandle) Wait() (*os.ProcessState, error) {
	<-m.done
	// NOTE: Careful when using this object: no way to initialize an os.ProcessState
	// so the content of the object should not be relied on.
	state := os.ProcessState{}
	return &state, nil
}

func (m *mockProcessHandle) kill() {
	close(m.done)
}

func (m *mockProcessInfo) MemoryInfo() (*process.MemoryInfoStat, error) {
	return &process.MemoryInfoStat{
		RSS: 1000,
	}, nil
}

func TestNewProcessWatcher(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	t.Run("no configured metrics", func(t *testing.T) {
		t.Parallel()
		metricsRegistry := mockWatcherMetricsRegistry{}
		cfg := config.LspwatchConfig{}
		_, err := NewProcessWatcher(&metricsRegistry, &cfg, logger)
		if err != nil {
			t.Fatalf("expected no errors creating process watcher, got '%v'", err)
		}

		if len(metricsRegistry.enableMetricCalls) != 1 {
			t.Fatalf("expected 1 EnableMetric call, got %d", len(metricsRegistry.enableMetricCalls))
		}

		if metricsRegistry.enableMetricCalls[0] != telemetry.ServerRSS {
			t.Fatalf("expected EnableMetric to be called with %s, got %s", telemetry.ServerRSS, metricsRegistry.enableMetricCalls[0])
		}
	})

	t.Run("explicitly no metrics enabled in config", func(t *testing.T) {
		t.Parallel()
		metricsRegistry := mockWatcherMetricsRegistry{}
		cfg := config.LspwatchConfig{
			Metrics: &[]string{},
		}
		_, err := NewProcessWatcher(&metricsRegistry, &cfg, logger)
		if err != nil {
			t.Fatalf("expected no errors creating process watcher, got '%v'", err)
		}

		if len(metricsRegistry.enableMetricCalls) != 0 {
			t.Fatalf("expected 0 EnableMetric calls, got %d", len(metricsRegistry.enableMetricCalls))
		}
	})

	t.Run("with configured metrics", func(t *testing.T) {
		t.Parallel()
		metricName := "some.metric"
		metricsRegistry := mockWatcherMetricsRegistry{}
		cfg := config.LspwatchConfig{
			Metrics: &[]string{metricName},
		}
		_, err := NewProcessWatcher(&metricsRegistry, &cfg, logger)
		if err != nil {
			t.Fatalf("expected no errors creating process watcher, got '%v'", err)
		}

		if len(metricsRegistry.enableMetricCalls) != 1 {
			t.Fatalf("expected 1 EnableMetric call, got %d", len(metricsRegistry.enableMetricCalls))
		}

		if metricsRegistry.enableMetricCalls[0] != telemetry.AvailableMetric(metricName) {
			t.Fatalf("expected EnableMetric to be called with %s, got %s", metricName, metricsRegistry.enableMetricCalls[0])
		}
	})
}

func TestProcessWatcher(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	processInfo := mockProcessInfo{}
	t.Run("emits metrics on interval and shuts down", func(t *testing.T) {
		t.Parallel()
		processHandle := mockProcessHandle{}
		metricsRegistry := mockWatcherMetricsRegistry{mu: sync.Mutex{}}
		pollingIntervalSeconds := 1
		cfg := config.LspwatchConfig{
			PollingInterval: &pollingIntervalSeconds,
		}
		processWatcher, err := NewProcessWatcher(&metricsRegistry, &cfg, logger)
		if err != nil {
			t.Fatalf("expected no errors creating process watcher, got '%v'", err)
		}

		err = processWatcher.Start(&processHandle, &processInfo)
		if err != nil {
			t.Fatalf("expected no errors starting process watcher, got '%v'", err)
		}

		sleepDuration := (time.Duration(pollingIntervalSeconds*2) * time.Second) + 500*time.Millisecond
		time.Sleep(sleepDuration)

		metricsRegistry.mu.Lock()
		if len(metricsRegistry.emitMetricCalls) != 2 {
			t.Fatalf("expected 2 EmitMetric calls, got %d", len(metricsRegistry.emitMetricCalls))
		}
		if metricsRegistry.emitMetricCalls[0].Name != string(telemetry.ServerRSS) {
			t.Fatalf("expected EmitMetric to be called with %s, got %s", telemetry.ServerRSS, metricsRegistry.emitMetricCalls[0].Name)
		}
		metricsRegistry.mu.Unlock()

		err = processWatcher.Shutdown()
		if err != nil {
			t.Fatalf("expected no errors shutting down process watcher, got '%v'", err)
		}
		testutil.AssertExitsBefore(
			t, "waiting for process watcher shutdown",
			func() { processWatcher.Wait() },
			1*time.Second,
		)
	})

	t.Run("correctly handles process exit", func(t *testing.T) {
		t.Parallel()
		processHandle := mockProcessHandle{done: make(chan struct{})}
		metricsRegistry := mockWatcherMetricsRegistry{mu: sync.Mutex{}}
		pollingIntervalSeconds := 1
		cfg := config.LspwatchConfig{
			PollingInterval: &pollingIntervalSeconds,
		}
		processWatcher, err := NewProcessWatcher(&metricsRegistry, &cfg, logger)
		if err != nil {
			t.Fatalf("expected no errors creating process watcher, got '%v'", err)
		}

		err = processWatcher.Start(&processHandle, &processInfo)
		if err != nil {
			t.Fatalf("expected no errors starting process watcher, got '%v'", err)
		}

		processHandle.kill()
		time.Sleep(500 * time.Millisecond)
		metricsRegistry.mu.Lock()
		initialEmitCount := len(metricsRegistry.emitMetricCalls)
		metricsRegistry.mu.Unlock()
		testutil.AssertExitsBefore(
			t, "waiting for process watcher exit notification",
			func() { <-processWatcher.ProcessExited() },
			100*time.Millisecond,
		)

		time.Sleep(2 * time.Second)

		metricsRegistry.mu.Lock()
		diff := len(metricsRegistry.emitMetricCalls) - initialEmitCount
		if diff > 0 {
			t.Errorf("expected no additional EmitMetric calls, got %d more", diff)
		}
		metricsRegistry.mu.Unlock()

		err = processWatcher.Shutdown()
		if err != nil {
			t.Fatalf("expected no errors shutting down process watcher, got '%v'", err)
		}

		testutil.AssertExitsBefore(
			t, "waiting for process watcher shutdown",
			func() { processWatcher.Wait() },
			500*time.Millisecond,
		)
	})
}
