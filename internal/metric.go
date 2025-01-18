package internal

type MetricKind int

const (
	Counter MetricKind = iota
	Gauge
	Histogram
)

type Tag struct {
	Key   string
	Value string
}

type MetricRecording struct {
	Name      string
	Timestamp int64
	Value     float64
	Tags      *map[string]string
}

type MetricsExporter interface {
	RegisterMetric(kind MetricKind, name string, description string, unit string) error
	EmitMetric(metric MetricRecording) error
	Shutdown() error
}

func NewTag(key string, value string) {}

func NewMetricRecording(
	name string,
	timestamp int64,
	value float64,
	tags ...Tag,
) MetricRecording {
	// TODO: tags
	tagsMap := make(map[string]string)
	return MetricRecording{
		Name:      name,
		Timestamp: timestamp,
		Value:     value,
		Tags:      &tagsMap,
	}
}
