package telemetry

import (
	"fmt"
)

// TODO: I am not so sure about my decision to use async Shutdown().
// I will probably undo this in a future PR.
type MetricsExporter interface {
	RegisterMetric(registration MetricRegistration) error
	EmitMetric(metric MetricRecording) error
	SetGlobalTags(tags ...Tag)

	// Must be idempotent and non-blocking. Use Wait() to block until shutdown is complete.
	Shutdown() error
	// Asynchronously starts the exporter.
	Start() error
	// Block until the exporter has shut down.
	Wait()
	// Release any held resources like open log files.
	Release() error
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

const (
	OS             AvailableTag = "os"
	LanguageServer AvailableTag = "language_server"
	User           AvailableTag = "user"
	RAM            AvailableTag = "ram"
)

type AvailableMetric string
type AvailableTag string
type MetricKind int

type TagValue string

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
	Tags      *map[string]TagValue
}

type Tag struct {
	Key   string
	Value TagValue
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

func NewMetricsRegistry(exporter MetricsExporter, availableMetrics map[AvailableMetric]MetricRegistration) MetricsRegistry {
	return MetricsRegistry{
		available: availableMetrics,
		enabled:   make(map[AvailableMetric]bool),
		exporter:  exporter,
	}
}

func NewTag(key string, value TagValue) Tag {
	return Tag{
		Key:   key,
		Value: value,
	}
}

func NewMetricRecording(
	name AvailableMetric,
	timestamp int64,
	value float64,
	tags ...Tag,
) MetricRecording {
	tagsMap := make(map[string]TagValue)
	for _, tag := range tags {
		tagsMap[tag.Key] = tag.Value
	}

	return MetricRecording{
		Name:      string(name),
		Timestamp: timestamp,
		Value:     value,
		Tags:      &tagsMap,
	}
}
