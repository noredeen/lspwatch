package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/bombsimon/logrusr/v4"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type OTelMetricsExporter struct {
	meterProvider *sdkmetric.MeterProvider
	meter         metric.Meter
	histograms    map[string]metric.Float64Histogram
}

var _ MetricsExporter = &OTelMetricsExporter{}

func (ome *OTelMetricsExporter) RegisterMetric(kind MetricKind, name string, description string, unit string) error {
	switch kind {
	case Histogram:
		hist, err := ome.meter.Float64Histogram(
			name,
			metric.WithDescription(description),
			metric.WithUnit(unit),
		)
		if err != nil {
			return err
		}

		ome.histograms[name] = hist
	}

	return nil
}

func (ome *OTelMetricsExporter) EmitMetric(metricPoint MetricRecording) error {
	if histogram, ok := ome.histograms[metricPoint.Name]; ok {
		// TODO: add timeout
		histogram.Record(context.Background(), metricPoint.Value)
		return nil
	} else {
		return fmt.Errorf("histogram not found for metric: %s", metricPoint.Name)
	}
}

// NOTE: Might have to rework this into invoking a function stored in the struct.
func (ome *OTelMetricsExporter) Shutdown() error {
	return ome.meterProvider.Shutdown(context.Background())
}

func newResource() (*resource.Resource, error) {
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("lspwatch"),
			semconv.ServiceVersion("0.1.0"),
			semconv.DeploymentEnvironment("production"),
		),
	)
}

func newMeterProvider(res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	// WARN: All configuration from With..() functions can be overriden
	// by setting environment vars
	metricExporter, err := otlpmetricgrpc.New(
		context.Background(),
		otlpmetricgrpc.WithInsecure(),
		// TODO: Add these options for configuration
		// otlpmetrichttp.WithEndpoint("localhost:4318"),
		// otlpmetricgrpc.WithEndpointURL("http://localhost:4317/v1/metrics"),
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
func NewOTelMetricsExporter(logger *logrus.Logger) (*OTelMetricsExporter, error) {
	logr := logrusr.New(logger)
	otel.SetLogger(logr)

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

	return &OTelMetricsExporter{
		meterProvider: meterProvider,
		meter:         meter,
	}, nil
}
