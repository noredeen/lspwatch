package telemetry

import (
	"testing"
	"time"
)

// type EmitMetricCall struct {
// 	MetricArg MetricRecording
// 	ErrorRet  error
// }

// type RegisterMetricCall struct {
// 	MetricArg MetricRegistration
// 	ErrorRet  error
// }

type MockMetricsExporter struct {
	emitMetricCalls     []MetricRecording
	registerMetricCalls []MetricRegistration
}

var _ MetricsExporter = &MockMetricsExporter{}

func (m *MockMetricsExporter) EmitMetric(metric MetricRecording) error {
	m.emitMetricCalls = append(m.emitMetricCalls, metric)
	return nil
}

func (m *MockMetricsExporter) RegisterMetric(metric MetricRegistration) error {
	m.registerMetricCalls = append(m.registerMetricCalls, metric)
	return nil
}

func (m *MockMetricsExporter) resetCalls() {
	m.emitMetricCalls = []MetricRecording{}
	m.registerMetricCalls = []MetricRegistration{}
}

func (m *MockMetricsExporter) SetGlobalTags(tags ...Tag) {}

func (m *MockMetricsExporter) Start() error { return nil }

func (m *MockMetricsExporter) Shutdown() error { return nil }

func (m *MockMetricsExporter) Wait() {}

func (m *MockMetricsExporter) Release() error { return nil }

func TestMetricsRegistry_IsMetricEnabled(t *testing.T) {
	registry := MetricsRegistry{
		registered: map[AvailableMetric]MetricRegistration{
			AvailableMetric("foo"): {
				Kind:        Histogram,
				Description: "foo",
				Name:        "lspwatch.foo",
				Unit:        "s",
			},
			AvailableMetric("bar"): {
				Kind:        Histogram,
				Description: "bar",
				Name:        "lspwatch.bar",
				Unit:        "s",
			},
		},
		enabled: map[AvailableMetric]bool{
			AvailableMetric("foo"): true,
		},
	}

	if !registry.IsMetricEnabled(AvailableMetric("foo")) {
		t.Errorf("expected metric 'foo' to be enabled")
	}

	if registry.IsMetricEnabled(AvailableMetric("bar")) {
		t.Errorf("expected metric 'bar' NOT to be enabled")
	}

	if registry.IsMetricEnabled(AvailableMetric("baz")) {
		t.Errorf("expected metric 'baz' NOT to be enabled")
	}
}

func TestMetricsRegistry_EnableMetric(t *testing.T) {
	exporter := &MockMetricsExporter{}
	registry := NewMetricsRegistry(
		exporter,
		map[AvailableMetric]MetricRegistration{
			AvailableMetric("foo"): {
				Kind:        Histogram,
				Description: "foo",
				Name:        "lspwatch.foo",
				Unit:        "s",
			},
		},
	)

	err := registry.EnableMetric(AvailableMetric("foo"))
	if err != nil {
		t.Errorf("expected no error enabling metric 'foo', got %v", err)
	}

	if !registry.IsMetricEnabled(AvailableMetric("foo")) {
		t.Errorf("expected metric 'foo' to be enabled")
	}

	if len(exporter.registerMetricCalls) == 1 {
		if exporter.registerMetricCalls[0].Name != "lspwatch.foo" {
			t.Errorf("expected RegisterMetric to be called on exporter with metric 'foo'")
		}
	} else {
		t.Errorf("expected 1 register metric call, got %d", len(exporter.registerMetricCalls))
	}

	exporter.resetCalls()

	err = registry.EnableMetric(AvailableMetric("baz"))
	if err == nil {
		t.Errorf("expected error enabling metric 'baz', got nil")
	}

	if len(exporter.registerMetricCalls) != 0 {
		t.Errorf("expected no register metric calls, got %d", len(exporter.registerMetricCalls))
	}
}

func TestMetricsRegistry_EmitMetric(t *testing.T) {
	t.Run("emits metric if enabled", func(t *testing.T) {
		t.Parallel()
		exporter := &MockMetricsExporter{}
		registry := MetricsRegistry{
			exporter: exporter,
			registered: map[AvailableMetric]MetricRegistration{
				AvailableMetric("foo"): {
					Kind:        Histogram,
					Description: "foo",
					Name:        "lspwatch.foo",
					Unit:        "s",
				},
			},
			enabled: map[AvailableMetric]bool{
				AvailableMetric("foo"): true,
			},
		}

		now := time.Now().Unix()
		registry.EmitMetric(
			NewMetricRecording(
				AvailableMetric("foo"),
				now,
				1.0,
			),
		)

		if len(exporter.emitMetricCalls) != 1 {
			t.Errorf("expected 1 emit metric call in exporter, got %d", len(exporter.emitMetricCalls))
		}

		if exporter.emitMetricCalls[0].Name != "lspwatch.foo" {
			t.Errorf("expected emit metric call in exporter to have metric name 'lspwatch.foo', got %s", exporter.emitMetricCalls[0].Name)
		}

		if exporter.emitMetricCalls[0].Timestamp != now {
			t.Errorf("expected emit metric call in exporter to have timestamp %d, got %d", now, exporter.emitMetricCalls[0].Timestamp)
		}
	})

	t.Run("does not emit metric if not registered or enabled", func(t *testing.T) {
		t.Parallel()
		exporter := &MockMetricsExporter{}
		registry := MetricsRegistry{
			exporter: exporter,
			registered: map[AvailableMetric]MetricRegistration{
				AvailableMetric("foo"): {
					Kind:        Histogram,
					Description: "foo",
					Name:        "lspwatch.foo",
					Unit:        "s",
				},
			},
			enabled: map[AvailableMetric]bool{},
		}

		err := registry.EmitMetric(
			NewMetricRecording(
				AvailableMetric("foo"),
				time.Now().Unix(),
				1.0,
			),
		)

		if err != nil {
			t.Fatalf("expected no error emitting disabled metric 'foo', got %v", err)
		}

		if len(exporter.emitMetricCalls) != 0 {
			t.Errorf("expected no emit metric call in exporter for disabled metric 'foo', got %d", len(exporter.emitMetricCalls))
		}

		err = registry.EmitMetric(
			NewMetricRecording(
				AvailableMetric("bar"),
				time.Now().Unix(),
				1.0,
			),
		)

		if err == nil {
			t.Fatalf("expected error emitting unregistered metric 'bar', got nil")
		}
	})
}
