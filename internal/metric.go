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
