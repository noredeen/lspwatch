package telemetry

import (
	"fmt"
)

type MetricsExporter interface {
	RegisterMetric(registration MetricRegistration) error
	EmitMetric(metric MetricRecording) error
	// Must be idempotent and non-blocking. Use Wait() to block until shutdown is complete.
	Shutdown() error
	Wait()
}

const (
	Counter MetricKind = iota
	Gauge
	Histogram
)

const (
	RequestDuration AvailableMetric = "lspwatch.request.duration"
	ServerRSS       AvailableMetric = "lspwatch.server.rss"
)

type AvailableMetric string
type MetricKind int

type MetricsRegistry struct {
	available map[AvailableMetric]MetricRegistration
	enabled   map[AvailableMetric]bool
	exporter  MetricsExporter
}

type MetricRegistration struct {
	Kind        MetricKind
	Name        string
	Description string
	Unit        string
}

type MetricRecording struct {
	Name      string
	Timestamp int64
	Value     float64
	Tags      *map[string]string
}

type Tag struct {
	Key   string
	Value string
}

func (mr *MetricsRegistry) RegisterMetric(metric AvailableMetric) error {
	registration, ok := mr.available[metric]
	if !ok {
		return fmt.Errorf("metric %s is not supported", metric)
	}

	err := mr.exporter.RegisterMetric(registration)
	if err != nil {
		return err
	}

	mr.enabled[metric] = true
	return nil
}

func (mr *MetricsRegistry) EmitMetric(metric MetricRecording) error {
	if !mr.enabled[AvailableMetric(metric.Name)] {
		return nil
	}

	return mr.exporter.EmitMetric(metric)
}

func (mr *MetricsRegistry) IsMetricEnabled(metric AvailableMetric) bool {
	return mr.enabled[AvailableMetric(metric)]
}

// Idempotent and non-blocking. Use Wait() to block until shutdown is complete.
func (mr *MetricsRegistry) Shutdown() error {
	return mr.exporter.Shutdown()
}

func (mr *MetricsRegistry) Wait() {
	mr.exporter.Wait()
}

func NewMetricsRegistry(exporter MetricsExporter, availableMetrics map[AvailableMetric]MetricRegistration) MetricsRegistry {
	return MetricsRegistry{
		available: availableMetrics,
		enabled:   make(map[AvailableMetric]bool),
		exporter:  exporter,
	}
}

func NewTag(key string, value string) Tag {
	return Tag{
		Key:   key,
		Value: value,
	}
}

func NewMetricRecording(
	name string,
	timestamp int64,
	value float64,
	tags ...Tag,
) MetricRecording {
	tagsMap := make(map[string]string)
	for _, tag := range tags {
		tagsMap[tag.Key] = tag.Value
	}

	return MetricRecording{
		Name:      name,
		Timestamp: timestamp,
		Value:     value,
		Tags:      &tagsMap,
	}
}
