package internal

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bombsimon/logrusr/v4"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
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
		// TODO: Add timeout
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

func newOTelMeterProvider(exporter sdkmetric.Exporter, res *resource.Resource) *sdkmetric.MeterProvider {
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(
				exporter,
				sdkmetric.WithInterval(30*time.Second),
			),
		),
	)

	return meterProvider
}

// TODO: Support config options
func newOTLPMetricsGRPCExporter(cfg *openTelemetryConfig) (sdkmetric.Exporter, error) {
	metricExporter, err := otlpmetricgrpc.New(
		context.Background(),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating OTLP gRPC metrics exporter: %v", err)
	}

	return metricExporter, nil
}

// TODO: Implement
func newOTLPMetricsHTTPExporter(cfg *openTelemetryConfig) (sdkmetric.Exporter, error) {
	return nil, nil
}

func newFileMetricsExporter(cfg *openTelemetryConfig) (sdkmetric.Exporter, error) {
	path := cfg.Directory

	if path[len(path)-1] != '/' {
		path += "/"
	}
	path += "lspwatch_metrics.json"

	metricsExporter, err := stdoutmetric.New(
		// TODO: use file
		stdoutmetric.WithWriter(os.Stdout),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating OTLP file metrics exporter: %v", err)
	}

	return metricsExporter, nil
}

// https://opentelemetry.io/docs/languages/go/getting-started/#initialize-the-opentelemetry-sdk
func NewOTelMetricsExporter(cfg *openTelemetryConfig, logger *logrus.Logger) (*OTelMetricsExporter, error) {
	logr := logrusr.New(logger)
	otel.SetLogger(logr)

	res, err := newResource()
	if err != nil {
		return nil, err
	}

	var metricExporter sdkmetric.Exporter
	switch cfg.Protocol {
	case "http":
		metricExporter, err = newOTLPMetricsHTTPExporter(cfg)
	case "grpc":
		metricExporter, err = newOTLPMetricsGRPCExporter(cfg)
	case "file":
		metricExporter, err = newFileMetricsExporter(cfg)
	}

	if err != nil {
		return nil, err
	}

	meterProvider := newOTelMeterProvider(metricExporter, res)

	meter := meterProvider.Meter("lspwatch")

	return &OTelMetricsExporter{
		meterProvider: meterProvider,
		meter:         meter,
	}, nil
}
