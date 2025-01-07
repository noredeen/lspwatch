package internal

import (
	"context"
	// "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"time"
)

type LatencyExporter interface {
	RecordLatency(time.Duration) error
	Shutdown() error
}

type OTelLatencyExporter struct {
	meterProvider *sdkmetric.MeterProvider
	histogram     metric.Float64Histogram
}

func (ole OTelLatencyExporter) RecordLatency(duration time.Duration) {
	ole.histogram.Record(context.Background(), duration.Seconds())
}

func (ole OTelLatencyExporter) Shutdown() error {
	return ole.meterProvider.Shutdown(context.Background())
}

func newResource() (*resource.Resource, error) {
	// TODO: Not sure what would go in here
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("my-service"),
			semconv.ServiceVersion("0.1.0"),
		))
}

func newMeterProvider(res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	metricExporter, err := stdoutmetric.New()
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter,
			sdkmetric.WithInterval(30*time.Second))),
	)

	return meterProvider, nil
}

func NewOTelLatencyExporter() (*OTelLatencyExporter, error) {
	res, err := newResource()
	if err != nil {
		return nil, err
	}

	meterProvider, err := newMeterProvider(res)
	if err != nil {
		return nil, err
	}

	meter := meterProvider.Meter("lspwatch")

	hist, err := meter.Float64Histogram(
		"request.duration",
		metric.WithDescription("The duration of an LSP request"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &OTelLatencyExporter{
		meterProvider: meterProvider,
		histogram:     hist,
	}, nil
}
