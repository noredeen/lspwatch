package telemetry

import (
	"fmt"
)

// TODO: I am not so sure about my decision to use async Start/Shutdown.
type MetricsExporter interface {
	RegisterMetric(registration MetricRegistration) error
	// Must be safe to call before Start() is called.
	EmitMetric(metric MetricRecording) error
	SetGlobalTags(tags ...Tag)

	// Must be idempotent and non-blocking. Use Wait() to block until shutdown is complete.
	Shutdown() error
	// Runs the exporter asynchronously.
	Start() error
	// Blocks until the exporter has flushed all held metrics and shut down.
	Wait()
	// Frees any resources (should be called after Wait)
	Release() error
}

const (
	Counter MetricKind = iota
	Gauge
	Histogram
)

const (
	RequestDuration AvailableMetric = "request.duration"
	ServerRSS       AvailableMetric = "server.rss"
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

type MetricsRegistry interface {
	EnableMetric(metric AvailableMetric) error
	EmitMetric(metric MetricRecording) error
	IsMetricEnabled(metric AvailableMetric) bool
}

type DefaultMetricsRegistry struct {
	registered map[AvailableMetric]MetricRegistration
	enabled    map[AvailableMetric]bool
	exporter   MetricsExporter
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

func (mr *DefaultMetricsRegistry) EnableMetric(metric AvailableMetric) error {
	registration, ok := mr.registered[metric]
	if !ok {
		return fmt.Errorf("cannot enable the unregistered metric %s", metric)
	}

	err := mr.exporter.RegisterMetric(registration)
	if err != nil {
		return err
	}

	mr.enabled[metric] = true
	return nil
}

// Skips emitting the metric if it's not enabled within the registry.
// Helpful for reducing nesting from IsMetricEnabled() checks.
func (mr *DefaultMetricsRegistry) EmitMetric(metric MetricRecording) error {
	registration, ok := mr.registered[AvailableMetric(metric.Name)]
	if !ok {
		return fmt.Errorf("cannot emit the unregistered metric %s", metric.Name)
	}

	if !mr.enabled[AvailableMetric(metric.Name)] {
		return nil
	}

	// Replace configuration name with export name.
	metric.Name = registration.Name

	return mr.exporter.EmitMetric(metric)
}

// Useful when producing the MetricRecording for EmitMetric() should be skipped.
func (mr *DefaultMetricsRegistry) IsMetricEnabled(metric AvailableMetric) bool {
	return mr.enabled[AvailableMetric(metric)]
}

func NewDefaultMetricsRegistry(exporter MetricsExporter, registeredMetrics map[AvailableMetric]MetricRegistration) DefaultMetricsRegistry {
	return DefaultMetricsRegistry{
		registered: registeredMetrics,
		enabled:    make(map[AvailableMetric]bool),
		exporter:   exporter,
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
