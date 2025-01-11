package internal

import (
	"context"
	// "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
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

var _ LatencyExporter = OTelLatencyExporter{}

func (ole OTelLatencyExporter) RecordLatency(duration time.Duration) error {
	ole.histogram.Record(context.Background(), duration.Seconds())
	return nil
}

// Might have to rework this into invoking a function stored in the struct
func (ole OTelLatencyExporter) Shutdown() error {
	return ole.meterProvider.Shutdown(context.Background())
}

func newResource() (*resource.Resource, error) {
	// TODO: Not sure what would go in here.
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("my-service"),
			semconv.ServiceVersion("0.1.0"),
		),
	)
}

/*
exporter: opentelemetry/datadog
opentelemetry:
	endpoint_url: ...
	timeout: ...
	proxy: ...
	retry: ...
	tls:
		client_ca: ...
		...
*/

func newMeterProvider(res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	// WARN: All configuration from With..() functions can be overriden
	// by setting environment vars
	metricExporter, err := otlpmetrichttp.New(
		context.Background(),
		// TODO:: Add these options for configuration.
		otlpmetrichttp.WithEndpointURL("http://localhost:4318/v1/metrics"),
		// otlpmetrichttp.WithHeaders(),
		// otlpmetrichttp.WithTLSClientConfig(),
		// otlpmetrichttp.WithTimeout(),
		// otlpmetrichttp.WithRetry(),
		// otlpmetrichttp.WithProxy(),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				metricExporter,
				sdkmetric.WithInterval(30*time.Second),
			),
		),
	)

	return meterProvider, nil
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator()
}

// https://opentelemetry.io/docs/languages/go/getting-started/#initialize-the-opentelemetry-sdk
func NewOTelLatencyExporter() (*OTelLatencyExporter, error) {
	// TODO: Do I need this propagator thing?
	// prop := newPropagator()
	// otel.SetTextMapPropagator(prop)

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
